package irc

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestOrderedNickFallbackOnWire(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	client, err := Dial(t.Context(), Config{Host: host, Port: port, Nick: "unused", Nicknames: [3]string{"primary", "secondary", "fallback"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if err := server.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(server)
	read := func(want string) {
		t.Helper()
		got, err := reader.ReadString('\n')
		if err != nil || got != want+"\r\n" {
			t.Fatalf("got %q (%v), want %q", got, err, want)
		}
	}
	send := func(line string) {
		t.Helper()
		if _, err := fmt.Fprint(server, line+"\r\n"); err != nil {
			t.Fatal(err)
		}
	}
	event := func(command, notice string) {
		t.Helper()
		select {
		case e := <-client.Events:
			if e.Err != nil || e.Notice != notice || (command != "" && (e.Message == nil || e.Message.Command != command)) {
				t.Fatalf("unexpected event: %+v", e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("event timeout")
		}
	}
	read("NICK primary")
	read("USER primary 0 * :primary")
	send(":irc.gloom.im 433 * primary :Nickname is already in use")
	read("NICK secondary")
	event("", "Nickname primary is in use, trying secondary")
	event("433", "")
	// A delayed duplicate must not skip the next preference.
	send(":irc.gloom.im 433 * primary :Nickname is already in use")
	event("433", "")
	send(":irc.gloom.im 432 * secondary :Erroneous nickname")
	read("NICK fallback")
	event("", "Nickname secondary is invalid, trying fallback")
	event("432", "")
	send(":irc.gloom.im 433 * fallback :Nickname is already in use")
	event("433", "")
	send("PING :exhausted")
	read("PONG :exhausted")
	if err := client.SetNicknames([3]string{"newname", "reserve"}); err != nil {
		t.Fatal(err)
	}
	read("NICK newname")
	send(":irc.gloom.im 432 * newname :Erroneous nickname")
	read("NICK reserve")
	event("", "Nickname newname is invalid, trying reserve")
	event("432", "")
	send(":irc.gloom.im 001 reserve :Welcome")
	event("001", "")
	if err := client.SetNicknames([3]string{"taken", "unused"}); err != nil {
		t.Fatal(err)
	}
	read("NICK taken")
	for _, numeric := range []string{"433", "432"} {
		send(":irc.gloom.im " + numeric + " reserve taken :Refused")
		event(numeric, "")
		send("PING :registered")
		read("PONG :registered")
	}
	send(":reserve!u@gloom.im NICK accepted")
	event("NICK", "")
	client.nickMu.Lock()
	nick := client.nick
	client.nickMu.Unlock()
	if nick != "accepted" {
		t.Fatalf("current nick = %q", nick)
	}
}

func TestNickListValidation(t *testing.T) {
	for _, nicks := range [][3]string{{}, {"good", "1bad"}, {"good", "", "gap"}, {"good", "bad\r\nQUIT"}, {"good", "ok", ":bad"}} {
		c := &Client{out: make(chan outgoing, 1), done: make(chan struct{}), nicknames: [3]string{"original"}, nick: "original"}
		if err := c.SetNicknames(nicks); err == nil {
			t.Fatalf("accepted %q", nicks)
		}
		if len(c.out) != 0 || c.nick != "original" || c.nicknames[0] != "original" {
			t.Fatal("invalid list changed state")
		}
		if nicks != ([3]string{}) {
			_, err := Dial(t.Context(), Config{Host: "127.0.0.1", Port: "1", Nick: "original", Nicknames: nicks})
			if err == nil || err.Error() != "invalid nickname list" {
				t.Fatalf("invalid list reached dial: %v", err)
			}
		}
	}
}

func TestNickRetryQueueFailure(t *testing.T) {
	c := &Client{out: make(chan outgoing, 1), done: make(chan struct{}), nicknames: [3]string{"primary", "secondary"}, nick: "primary"}
	c.out <- outgoing{line: "USER primary 0 * :primary"}
	if err := c.SetNicknames([3]string{"replacement"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue: %v", err)
	}
	notice, err := c.handleNick(&Message{Command: "433", Params: []string{"*", "primary", "Refused"}})
	if !errors.Is(err, ErrQueueFull) || notice != "" || c.nick != "primary" || c.nickIndex != 0 {
		t.Fatalf("failed retry changed state: %q, %v", notice, err)
	}
}

func TestSingleNickAndMalformedRepliesDoNotRetry(t *testing.T) {
	c := &Client{out: make(chan outgoing, 4), done: make(chan struct{}), nicknames: [3]string{"primary"}, nick: "primary"}
	for _, m := range []*Message{
		{Command: "433", Params: []string{"*", "primary", "Refused"}},
		{Command: "432", Params: []string{"*", "primary", "Refused"}},
		{Command: "433"},
		{Command: "001"},
		{Command: "NICK"},
	} {
		if notice, err := c.handleNick(m); notice != "" || err != nil {
			t.Fatalf("unexpected retry: %q, %v", notice, err)
		}
	}
	if len(c.out) != 0 {
		t.Fatal("unexpected nickname attempt")
	}
}
