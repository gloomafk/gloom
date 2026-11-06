package irc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type failedWriteConn struct {
	net.Conn
	deadlineErr error
	writes      int
}

func (c *failedWriteConn) SetWriteDeadline(time.Time) error { return c.deadlineErr }

func (c *failedWriteConn) Write(p []byte) (int, error) {
	c.writes++
	return len(p) - 1, nil
}

func TestWriteFailureReportsErrorAndCloses(t *testing.T) {
	deadlineErr := errors.New("deadline unavailable")
	for _, tc := range []struct {
		name        string
		deadlineErr error
		wantErr     error
		wantWrites  int
	}{
		{name: "deadline", deadlineErr: deadlineErr, wantErr: deadlineErr},
		{name: "short write", wantErr: io.ErrShortWrite, wantWrites: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := net.Pipe()
			t.Cleanup(func() { _ = peer.Close() })
			failed := &failedWriteConn{Conn: conn, deadlineErr: tc.deadlineErr}
			c := &Client{conn: failed, Events: make(chan Event, 1), Raw: make(chan Line, 1), out: make(chan outgoing, 1), done: make(chan struct{})}
			t.Cleanup(c.Close)
			written := make(chan error, 1)
			c.out <- outgoing{line: "QUIT", written: written}
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				c.writeLoop()
			}()
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("writer did not stop after a failed write")
			}
			select {
			case err := <-written:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("write acknowledgement = %v, want %v", err, tc.wantErr)
				}
			default:
				t.Fatal("failed write was not acknowledged")
			}
			select {
			case event := <-c.Events:
				if !errors.Is(event.Err, tc.wantErr) {
					t.Fatalf("connection error = %v, want %v", event.Err, tc.wantErr)
				}
			default:
				t.Fatal("connection error was not reported")
			}
			if failed.writes != tc.wantWrites || len(c.Raw) != 0 {
				t.Fatalf("writes = %d, raw lines = %d", failed.writes, len(c.Raw))
			}
			select {
			case <-c.done:
			default:
				t.Fatal("failed connection remained open")
			}
		})
	}
}

func TestAddressAndBoundaries(t *testing.T) {
	for i, args := range [][]string{{"irc.gloom.im", "6667"}, {"-TLS", "irc.gloom.im"}, {"irc.gloom.im", "+6697"}} {
		c, err := ParseAddress(args, "tester")
		if err != nil {
			t.Fatal(err)
		}
		wantPort := "6667"
		if i > 0 {
			wantPort = "6697"
		}
		if c.Host != "irc.gloom.im" || c.Port != wantPort || c.TLS != (i > 0) {
			t.Fatalf("ParseAddress(%q) = %+v, want irc.gloom.im:%s with TLS %t", args, c, wantPort, i > 0)
		}
	}
	for _, args := range [][]string{{}, {"irc.gloom.im", "0"}, {"irc.gloom.im", "65536"}, {"irc.gloom.im\r\nQUIT"}, {"irc.gloom.im", "bad"}, {"irc.gloom.im\tname"}, {"irc.gloom.im\x1b[31m"}, {"irc.gloom.im\u00a0name"}} {
		if _, err := ParseAddress(args, "tester"); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
	if ValidNick("x\r\nQUIT") || ValidNick("1bad") || Atom("#a\nQUIT") {
		t.Fatal("unsafe atom accepted")
	}
	if got := Clean("hello\x1b\x07\u009bworld"); got != "helloworld" {
		t.Fatalf("unsafe terminal content: %q", got)
	}
	if Fold("[Nick]\\^") != Fold("{nick}|~") {
		t.Fatal("RFC1459 folding mismatch")
	}
}

func TestCleanRemovesEscapePayloads(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"hello\x1b[31mred\x1b[0m world", "hellored world"},
		{"hello\x1b]52;c;Y2xpcGJvYXJk\x07 world", "hello world"},
		{"hello\x1b]8;;https://gloom.im\x1b\\link\x1b]8;;\x1b\\ world", "hellolink world"},
		{"hello\x1bPpayload\x1b\\ world", "hello world"},
		{"你好 👩‍💻\t\r\n\u2028world", "你好 👩‍💻world"},
	} {
		if got := Clean(tc.input); got != tc.want {
			t.Errorf("Clean(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
func TestRegistrationPingAndMessage(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(conn)
		for _, want := range []string{"NICK tester\r\n", "USER tester 0 * :tester\r\n"} {
			got, e := r.ReadString('\n')
			if e != nil || got != want {
				done <- fmt.Errorf("registration %q: %v", got, e)
				return
			}
		}
		_, err = fmt.Fprint(conn, "PING :probe\r\n:irc.gloom.im 001 tester :Welcome\r\n:friend!u@gloom.im PRIVMSG tester :hello\r\n")
		if err != nil {
			done <- err
			return
		}
		got, err := r.ReadString('\n')
		if err != nil || got != "PONG :probe\r\n" {
			done <- fmt.Errorf("pong %q: %v", got, err)
			return
		}
		done <- nil
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	c, err := Dial(t.Context(), Config{Host: host, Port: port, Nick: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Send("PRIVMSG x :hello\r\nQUIT") == nil || c.Send(strings.Repeat("x", 511)) == nil {
		t.Fatal("unsafe outbound line accepted")
	}
	for _, want := range []string{"001", "PRIVMSG"} {
		select {
		case e := <-c.Events:
			if e.Err != nil || e.Message == nil || e.Message.Command != want {
				t.Fatalf("expected %s, got %+v", want, e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("event timeout")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestQuitTimeoutClosesStalledConnection(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	c := &Client{conn: conn, Events: make(chan Event, 1), out: make(chan outgoing, 1), done: make(chan struct{})}
	defer c.Close()
	go c.writeLoop()
	started := time.Now()
	if err := c.Quit("goodbye"); err == nil {
		t.Fatal("stalled write reported success")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("quit exceeded its bounded wait")
	}
	select {
	case <-c.done:
	default:
		t.Fatal("stalled connection left open")
	}
}

func TestNewConfigDefaultsPorts(t *testing.T) {
	for _, tc := range []struct {
		port string
		tls  bool
		want Config
	}{
		{"", false, Config{Host: "irc.gloom.im", Port: "6667", Nick: "n"}},
		{"", true, Config{Host: "irc.gloom.im", Port: "6697", Nick: "n", TLS: true}},
		{"+7000", false, Config{Host: "irc.gloom.im", Port: "7000", Nick: "n", TLS: true}},
	} {
		got, err := NewConfig("irc.gloom.im", tc.port, tc.tls, "n")
		if err != nil || got != tc.want {
			t.Fatalf("NewConfig(%q, %t) = %+v, %v; want %+v", tc.port, tc.tls, got, err, tc.want)
		}
	}
	if _, err := NewConfig("", "", false, "n"); err == nil {
		t.Fatal("empty host accepted")
	}
}

func TestClosingStopsReadersAndClosesChannels(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		if conn, err := listener.Accept(); err == nil {
			defer conn.Close()
			_, _ = bufio.NewReader(conn).Discard(1)
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	c, err := Dial(t.Context(), Config{Host: host, Port: port, Nick: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	for _, drained := range []func() bool{
		func() bool { _, ok := <-c.Events; return !ok },
		func() bool { _, ok := <-c.Raw; return !ok },
	} {
		deadline := time.After(5 * time.Second)
		for {
			select {
			case <-deadline:
				t.Fatal("channel left open after Close")
			default:
			}
			if drained() {
				break
			}
		}
	}
}

func TestPongIsNotDroppedSilently(t *testing.T) {
	c := &Client{pong: make(chan outgoing, 1), done: make(chan struct{})}
	if err := c.sendPong("a"); err != nil {
		t.Fatal(err)
	}
	if err := c.sendPong("b"); err != ErrQueueFull {
		t.Fatalf("full pong queue returned %v, want ErrQueueFull", err)
	}
	if err := c.sendPong("x\r\nQUIT"); err == nil {
		t.Fatal("unsafe PING token accepted")
	}
}

func TestPongOvertakesQueuedLines(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	c := &Client{conn: client, Events: make(chan Event, 4), Raw: make(chan Line, 4), out: make(chan outgoing, 4), pong: make(chan outgoing, 4), done: make(chan struct{})}
	defer c.Close()
	if err := c.Send("PRIVMSG #a :queued first"); err != nil {
		t.Fatal(err)
	}
	if err := c.sendPong("probe"); err != nil {
		t.Fatal(err)
	}
	go c.writeLoop()
	_ = server.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(server)
	for _, want := range []string{"PONG :probe\r\n", "PRIVMSG #a :queued first\r\n"} {
		if got, err := r.ReadString('\n'); err != nil || got != want {
			t.Fatalf("wrote %q (%v), want %q", got, err, want)
		}
	}
}

func TestRealnameInRegistration(t *testing.T) {
	for _, tc := range []struct{ realname, want string }{
		{"", "USER tester 0 * :tester\r\n"},
		{"Ada Lovelace: tester", "USER tester 0 * :Ada Lovelace: tester\r\n"},
	} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		got := make(chan string, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			r := bufio.NewReader(conn)
			_, _ = r.ReadString('\n')
			line, _ := r.ReadString('\n')
			got <- line
		}()
		host, port, _ := net.SplitHostPort(listener.Addr().String())
		c, err := Dial(t.Context(), Config{Host: host, Port: port, Nick: "tester", Realname: tc.realname})
		if err != nil {
			t.Fatal(err)
		}
		if line := <-got; line != tc.want {
			t.Errorf("realname %q sent %q, want %q", tc.realname, line, tc.want)
		}
		c.Close()
		listener.Close()
	}
}

func TestInvalidRealnameIsRejected(t *testing.T) {
	for _, s := range []string{"a\r\nQUIT", "a\x00b", "red\x1b[31m", strings.Repeat("x", 101)} {
		if ValidRealname(s) {
			t.Errorf("accepted %q", s)
		}
		if _, err := Dial(t.Context(), Config{Host: "127.0.0.1", Port: "1", Nick: "tester", Realname: s}); err == nil || err.Error() != "invalid realname" {
			t.Errorf("Dial accepted %q: %v", s, err)
		}
	}
}
