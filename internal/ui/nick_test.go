package ui

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"gloom.im/gloom/internal/irc"
)

func TestNickPreferencesValidation(t *testing.T) {
	m := New("tester", false)
	m.command("/nick primary secondary fallback")
	want := [3]string{"primary", "secondary", "fallback"}
	if m.preferredNicks != want || m.nick != "primary" {
		t.Fatal("preferences not set")
	}
	for _, input := range []string{"/nick", "/nick one two three four", "/nick primary 1bad", "/nick primary secondary :bad", "/nick primary\r\nQUIT"} {
		m.command(input)
		if m.preferredNicks != want || m.nick != "primary" {
			t.Fatalf("invalid input changed preferences: %q", input)
		}
	}
	m.command("/nick single")
	if m.preferredNicks != ([3]string{"single"}) || m.nick != "single" {
		t.Fatal("single nick retained fallbacks")
	}
}

func TestNickPreferencesReachConnection(t *testing.T) {
	for _, duringDial := range []bool{false, true} {
		t.Run(map[bool]string{false: "before connect", true: "during dial"}[duringDial], func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			m := New("tester", false)
			t.Cleanup(m.Close)
			input := "/nick primary secondary fallback"
			if !duringDial {
				m.command(input)
			}
			cmd := m.command("/connect " + host + " " + port)
			if duringDial {
				m.command(input)
			}
			v := cmd().(connected)
			if v.err != nil {
				t.Fatal(v.err)
			}
			m.update(v)
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
				if err != nil || strings.TrimSuffix(got, "\r\n") != want {
					t.Fatalf("got %q (%v), want %q", got, err, want)
				}
			}
			initial := "primary"
			if duringDial {
				initial = "tester"
			}
			read("NICK " + initial)
			read("USER " + initial + " 0 * :" + initial)
			if duringDial {
				read("NICK primary")
			}
			if _, err := server.Write([]byte(":irc.gloom.im 433 * primary :Taken\r\n")); err != nil {
				t.Fatal(err)
			}
			read("NICK secondary")
			select {
			case e := <-m.client.Events:
				m.update(incoming{client: m.client, event: e, ok: true})
				if !strings.Contains(lastText(m), "Nickname primary is in use, trying secondary") {
					t.Fatalf("retry not shown: %q", lastText(m))
				}
			case <-time.After(5 * time.Second):
				t.Fatal("retry event timeout")
			}
		})
	}
}

func TestNickPreferencesKeepTLSAddress(t *testing.T) {
	m := New("tester", false)
	m.command("/nick primary secondary")
	m.command("/connect -TLS irc.gloom.im 6697")
	if !m.connecting {
		t.Fatal("TLS connection not scheduled")
	}
	m.Close()
	// Parsing remains independent of nickname preferences.
	c, err := irc.ParseAddress([]string{"-TLS", "irc.gloom.im", "6697"}, m.nick)
	if err != nil || !c.TLS || c.Port != "6697" {
		t.Fatalf("TLS address changed: %+v, %v", c, err)
	}
}
