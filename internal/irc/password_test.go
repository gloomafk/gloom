package irc

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPasswordAddress(t *testing.T) {
	for _, args := range [][]string{
		{"-pass", "example-password", "irc.gloom.im"},
		{"-TLS", "-pass", "example-password", "irc.gloom.im"},
		{"-pass", "example-password", "-tls", "irc.gloom.im"},
		{"-pass", "example-password", "irc.gloom.im", "+6697"},
	} {
		c, err := ParseAddress(args, "tester")
		if err != nil || c.Password != "example-password" || c.Host != "irc.gloom.im" {
			t.Fatalf("password options not parsed: %v", err)
		}
		if len(args) > 3 && (!c.TLS || c.Port != "6697") {
			t.Fatal("TLS option lost")
		}
	}
	for _, args := range [][]string{
		{"-pass"}, {"-pass", "example-password"}, {"-pass", "", "irc.gloom.im"},
		{"-unknown", "irc.gloom.im"}, {"-pass", "bad\r\nQUIT", "irc.gloom.im"},
	} {
		if _, err := ParseAddress(args, "tester"); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}

func TestInvalidPasswordBeforeDial(t *testing.T) {
	for _, password := range []string{"bad\r\nQUIT", "bad\x00", "bad\x01", "bad\x1b[31m", strings.Repeat("x", 505)} {
		_, err := Dial(t.Context(), Config{Host: "127.0.0.1", Port: "1", Nick: "tester", Password: password})
		if err == nil || err.Error() != "invalid server password" {
			t.Fatalf("wanted password validation before dial: %v", err)
		}
	}
	if !ValidPassword(strings.Repeat("x", 504)) {
		t.Fatal("maximum password rejected")
	}
}

func TestPasswordRegistrationAndDebug(t *testing.T) {
	for _, password := range []string{"", "example-password", ":example password"} {
		t.Run(password, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			client, err := Dial(t.Context(), Config{Host: host, Port: port, Nick: "tester", Password: password})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			reader := bufio.NewReader(conn)
			want := []string{"NICK tester", "USER tester 0 * :tester"}
			if password != "" {
				want = append([]string{"PASS :" + password}, want...)
			}
			for _, line := range want {
				got, err := reader.ReadString('\n')
				if err != nil || got != line+"\r\n" {
					t.Fatalf("unexpected registration line: %q, %v", got, err)
				}
				select {
				case raw := <-client.Raw:
					if strings.HasPrefix(line, "PASS ") {
						line = "PASS (password hidden)"
					}
					if !raw.Out || raw.Text != line {
						t.Fatalf("unexpected debug line: %q", raw.Text)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("missing debug line")
				}
			}
		})
	}
}

func TestUntrustedTLSDoesNotSendPassword(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP request") }))
	defer server.Close()
	host, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	client, err := Dial(t.Context(), Config{Host: host, Port: port, Nick: "tester", TLS: true, Password: "example-password"})
	if err == nil || client != nil {
		t.Fatal("untrusted certificate accepted")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("unexpected TLS error: %v", err)
	}
}

func TestPassDebugRedaction(t *testing.T) {
	for _, line := range []string{"PASS :example-password", "pass example-password", "@tag=x :irc.gloom.im PASS :example-password"} {
		c := &Client{Raw: make(chan Line, 1)}
		c.tap(Line{Text: line, Out: true})
		if got := <-c.Raw; got.Text != "PASS (password hidden)" {
			t.Fatal("PASS debug traffic exposed")
		}
	}
}

func TestServerPasswordWithSASL(t *testing.T) {
	parsed, err := ParseAddress(strings.Fields("-sasl-user account -pass example-password -tls -sasl-pass secret irc.gloom.im"), "tester")
	if err != nil || parsed.Password != "example-password" || !parsed.SASL.Enabled() || !parsed.TLS {
		t.Fatalf("combined authentication options failed: %v", err)
	}
	steps := []string{
		"> CAP LS 302", "> PASS :example-password", "> NICK tester", "> USER tester 0 * :tester",
		"< :irc.gloom.im CAP * LS :sasl=PLAIN",
		"> CAP REQ :sasl", "< :irc.gloom.im CAP tester ACK :sasl",
		"> AUTHENTICATE PLAIN", "< AUTHENTICATE +",
		"> AUTHENTICATE " + plainPayload(parsed.SASL),
		"< :irc.gloom.im 903 tester :SASL authentication successful",
		"> CAP END", "< :irc.gloom.im 001 tester :Welcome",
	}
	conf, served := converse(t, steps)
	conf.Password, conf.SASL = parsed.Password, parsed.SASL
	c, err := Dial(t.Context(), conf)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	notices, _ := untilWelcome(t, c)
	if len(notices) != 1 || notices[0] != "SASL authentication succeeded." {
		t.Fatalf("SASL did not succeed: %q", notices)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	c.Close()
	for line := range c.Raw {
		if strings.Contains(line.Text, "example-password") || strings.Contains(line.Text, plainPayload(parsed.SASL)) {
			t.Fatal("authentication credentials exposed in debug traffic")
		}
	}
}
