package irc

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var saslTestRoots sync.Once

// trustedTLSListener uses a test certificate in memory. Only this test process
// trusts it; production Dial still uses the system trust store.
func trustedTLSListener(t *testing.T, listener net.Listener) net.Listener {
	t.Helper()
	fixture := httptest.NewTLSServer(nil)
	t.Cleanup(fixture.Close)
	t.Setenv("GODEBUG", "x509usefallbackroots=1")
	saslTestRoots.Do(func() {
		roots := x509.NewCertPool()
		roots.AddCert(fixture.Certificate())
		x509.SetFallbackRoots(roots)
	})
	return tls.NewListener(listener, fixture.TLS.Clone())
}

// converse runs a scripted server on a loopback listener. A step starting
// with "> " is a line the client must send next, and "< " a line the server
// sends. The returned channel reports the first mismatch, or nil.
func converse(t *testing.T, steps []string) (Config, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	useTLS := len(steps) > 0 && steps[0] == "> CAP LS 302"
	if useTLS {
		listener = trustedTLSListener(t, listener)
	}
	result := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(conn)
		for _, step := range steps {
			if line, ok := strings.CutPrefix(step, "< "); ok {
				if _, err := fmt.Fprint(conn, line+"\r\n"); err != nil {
					result <- err
					return
				}
				continue
			}
			want := strings.TrimPrefix(step, "> ")
			got, err := r.ReadString('\n')
			if err != nil || got != want+"\r\n" {
				result <- fmt.Errorf("client sent %q (%v), want %q", got, err, want)
				return
			}
		}
		result <- nil
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	return Config{Host: host, Port: port, Nick: "tester", TLS: useTLS, SASL: SASL{Mechanism: "PLAIN", User: "account", Pass: "secret"}}, result
}

// untilWelcome collects the notices and message commands the client reports
// up to and including 001.
func untilWelcome(t *testing.T, c *Client) (notices, commands []string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-c.Events:
			if !ok || e.Err != nil {
				t.Fatalf("connection ended early: %v (notices %q)", e.Err, notices)
			}
			if e.Notice != "" {
				notices = append(notices, e.Notice)
				continue
			}
			commands = append(commands, e.Message.Command)
			if e.Message.Command == "001" {
				return notices, commands
			}
		case <-timeout:
			t.Fatalf("no 001; notices %q, commands %q", notices, commands)
		}
	}
}

var registration = []string{"> CAP LS 302", "> NICK tester", "> USER tester 0 * :tester"}

func script(steps ...string) []string { return append(slices.Clone(registration), steps...) }

func TestSASLPlainSucceeds(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("\x00account\x00secret"))
	conf, served := converse(t, script(
		"< :irc.gloom.im CAP * LS * :multi-prefix away-notify",
		"< :irc.gloom.im CAP * LS :sasl=EXTERNAL,PLAIN server-time",
		"> CAP REQ :sasl",
		"< :irc.gloom.im CAP tester ACK :sasl",
		"> AUTHENTICATE PLAIN",
		"< AUTHENTICATE +",
		"> AUTHENTICATE "+payload,
		"< :irc.gloom.im 900 tester tester!tester@gloom.im account :You are now logged in as account",
		"< :irc.gloom.im 903 tester :SASL authentication successful",
		"> CAP END",
		"< :irc.gloom.im 001 tester :Welcome",
	))
	c, err := Dial(t.Context(), conf)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	notices, commands := untilWelcome(t, c)
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(notices, []string{"SASL authentication succeeded."}) {
		t.Fatalf("notices = %q", notices)
	}
	// CAP, AUTHENTICATE and 903 belong to the negotiation; 900 names the account.
	if !slices.Equal(commands, []string{"900", "001"}) {
		t.Fatalf("commands = %q, want 900 and 001", commands)
	}
	c.Close()
	sawHidden := false
	for line := range c.Raw {
		if strings.Contains(line.Text, payload) || strings.Contains(line.Text, "secret") {
			t.Fatalf("credentials reached the debug window: %q", line.Text)
		}
		sawHidden = sawHidden || line.Text == hiddenPayload
	}
	if !sawHidden {
		t.Fatal("the credentials line was not shown as hidden")
	}
	c.sasl.mu.Lock()
	defer c.sasl.mu.Unlock()
	if c.sasl.creds != (SASL{}) {
		t.Fatal("credentials kept after negotiation")
	}
}

func TestSASLEndsWithoutAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name   string
		steps  []string
		notice string
	}{
		{
			name: "rejected credentials",
			steps: []string{
				"< :irc.gloom.im CAP * LS :sasl",
				"> CAP REQ :sasl",
				"< :irc.gloom.im CAP tester ACK :sasl",
				"> AUTHENTICATE PLAIN",
				"< AUTHENTICATE +",
				"> AUTHENTICATE " + base64.StdEncoding.EncodeToString([]byte("\x00account\x00secret")),
				"< :irc.gloom.im 904 tester :SASL authentication failed",
				"> CAP END",
			},
			notice: "SASL authentication failed: the server rejected the account or password. Continuing without authentication.",
		},
		{
			name: "numeric 905",
			steps: []string{
				"< :irc.gloom.im CAP * LS :sasl",
				"> CAP REQ :sasl",
				"< :irc.gloom.im CAP tester ACK :sasl",
				"> AUTHENTICATE PLAIN",
				"< :irc.gloom.im 905 tester :Result",
				"> CAP END",
			},
			notice: "SASL authentication failed: the credentials are too long for the server. Continuing without authentication.",
		},
		{
			name: "numeric 906",
			steps: []string{
				"< :irc.gloom.im CAP * LS :sasl",
				"> CAP REQ :sasl",
				"< :irc.gloom.im CAP tester ACK :sasl",
				"> AUTHENTICATE PLAIN",
				"< :irc.gloom.im 906 tester :Result",
				"> CAP END",
			},
			notice: "SASL authentication was aborted. Continuing without authentication.",
		},
		{
			name: "numeric 907",
			steps: []string{
				"< :irc.gloom.im CAP * LS :sasl",
				"> CAP REQ :sasl",
				"< :irc.gloom.im CAP tester ACK :sasl",
				"> AUTHENTICATE PLAIN",
				"< :irc.gloom.im 907 tester :Result",
				"> CAP END",
			},
			notice: "Already authenticated with SASL.",
		},
		{
			name: "nickname locked",
			steps: []string{
				"< :irc.gloom.im CAP * LS :sasl",
				"> CAP REQ :sasl",
				"< :irc.gloom.im CAP tester ACK :sasl",
				"> AUTHENTICATE PLAIN",
				"< :irc.gloom.im 902 tester :You must use a nick assigned to you",
				"> CAP END",
			},
			notice: "SASL authentication failed: You must use a nick assigned to you. Continuing without authentication.",
		},
		{
			name:   "no sasl capability",
			steps:  []string{"< :irc.gloom.im CAP * LS :multi-prefix", "> CAP END"},
			notice: "The server does not offer SASL. Continuing without authentication.",
		},
		{
			name:   "no PLAIN mechanism",
			steps:  []string{"< :irc.gloom.im CAP * LS :sasl=EXTERNAL", "> CAP END"},
			notice: "The server does not offer SASL PLAIN, only EXTERNAL. Continuing without authentication.",
		},
		{
			name:   "capability refused",
			steps:  []string{"< :irc.gloom.im CAP * LS :sasl", "> CAP REQ :sasl", "< :irc.gloom.im CAP * NAK :sasl", "> CAP END"},
			notice: "The server refused the SASL capability. Continuing without authentication.",
		},
		{
			name:   "capability not acknowledged",
			steps:  []string{"< :irc.gloom.im CAP * LS :sasl", "> CAP REQ :sasl", "< :irc.gloom.im CAP * ACK :multi-prefix", "> CAP END"},
			notice: "The server did not acknowledge the SASL capability. Continuing without authentication.",
		},
		{
			name:   "no capability negotiation",
			steps:  []string{"< :irc.gloom.im 421 tester CAP :Unknown command"},
			notice: "The server does not support capability negotiation. Continuing without authentication.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conf, served := converse(t, script(append(tc.steps, "< :irc.gloom.im 001 tester :Welcome")...))
			c, err := Dial(t.Context(), conf)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			notices, commands := untilWelcome(t, c)
			if err := <-served; err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(notices, []string{tc.notice}) {
				t.Fatalf("notices = %q, want %q", notices, tc.notice)
			}
			if !slices.Equal(commands, []string{"001"}) {
				t.Fatalf("negotiation lines reached the UI: %q", commands)
			}
		})
	}
}

func TestSASLTimeoutAbortsAndEndsNegotiation(t *testing.T) {
	server, conn := net.Pipe()
	defer server.Close()
	c := &Client{conn: conn, Events: make(chan Event, 4), Raw: make(chan Line, 16), out: make(chan outgoing, 16), pong: make(chan outgoing, 1), done: make(chan struct{})}
	defer c.Close()
	c.startNegotiation(SASL{Mechanism: "PLAIN", User: "account", Pass: "secret"}, saslTimeout)
	c.sasl.step = stepMechanism
	c.saslTimedOut()
	if e := <-c.Events; e.Notice != "SASL authentication timed out. Continuing without authentication." {
		t.Fatalf("notice = %q", e.Notice)
	}
	for _, want := range []string{"AUTHENTICATE *", "CAP END"} {
		if got := (<-c.out).line; got != want {
			t.Fatalf("sent %q, want %q", got, want)
		}
	}
	// A timer that fires after the end changes nothing.
	c.saslTimedOut()
	if len(c.out) != 0 || len(c.Events) != 0 {
		t.Fatal("a second timeout sent more lines")
	}
	if c.sasl.step != stepDone || c.sasl.creds != (SASL{}) {
		t.Fatal("negotiation not ended")
	}
}

// TestSASLTimerFiresAfterSetup lets the real timer fire at once, so the race
// detector checks that its callback is ordered after negotiation is attached.
func TestSASLTimerFiresAfterSetup(t *testing.T) {
	c := &Client{Events: make(chan Event, 1), out: make(chan outgoing, 4), done: make(chan struct{})}
	defer close(c.done)
	c.startNegotiation(SASL{Mechanism: "PLAIN", User: "account", Pass: "secret"}, time.Nanosecond)
	select {
	case e := <-c.Events:
		if e.Notice != "SASL authentication timed out. Continuing without authentication." {
			t.Fatalf("notice = %q", e.Notice)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timer did not fire")
	}
	if got := (<-c.out).line; got != "CAP END" {
		t.Fatalf("sent %q, want CAP END", got)
	}
}

func TestSASLTimeoutAfterConnectionEndsIsSilent(t *testing.T) {
	c := &Client{Events: make(chan Event), out: make(chan outgoing, 4), done: make(chan struct{})}
	c.startNegotiation(SASL{Mechanism: "PLAIN", User: "account", Pass: "secret"}, saslTimeout)
	c.sasl.stop()
	// Events is unbuffered and unread: an emit here would block the test.
	c.saslTimedOut()
	if len(c.out) != 0 {
		t.Fatal("a timeout after the connection ended sent lines")
	}
}

func TestPlainConnectionSkipsCapabilityNegotiation(t *testing.T) {
	conf, served := converse(t, []string{"> NICK tester", "> USER tester 0 * :tester", "< :irc.gloom.im 001 tester :Welcome"})
	conf.SASL = SASL{}
	c, err := Dial(t.Context(), conf)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if notices, _ := untilWelcome(t, c); len(notices) != 0 {
		t.Fatalf("notices without SASL: %q", notices)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticateLinesSplitAt400Bytes(t *testing.T) {
	for _, tc := range []struct {
		size  int
		sizes []int
	}{
		{size: 12, sizes: []int{12}},
		{size: 400, sizes: []int{400, 1}},
		{size: 401, sizes: []int{400, 1}},
		{size: 800, sizes: []int{400, 400, 1}},
	} {
		lines := authenticateLines(strings.Repeat("A", tc.size))
		var sizes []int
		for _, line := range lines {
			payload, ok := strings.CutPrefix(line, "AUTHENTICATE ")
			if !ok {
				t.Fatalf("not an AUTHENTICATE line: %q", line)
			}
			sizes = append(sizes, len(payload))
		}
		if !slices.Equal(sizes, tc.sizes) {
			t.Errorf("payload of %d split into %v, want %v", tc.size, sizes, tc.sizes)
		}
		if tc.size%400 == 0 && lines[len(lines)-1] != "AUTHENTICATE +" {
			t.Errorf("payload of %d not ended with +", tc.size)
		}
	}
}

func TestParseAddressSASL(t *testing.T) {
	c, err := ParseAddress(strings.Fields("-tls -sasl-user account -sasl-pass secret -sasl-mech plain irc.gloom.im 6697"), "tester")
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Host: "irc.gloom.im", Port: "6697", Nick: "tester", TLS: true, SASL: SASL{Mechanism: "PLAIN", User: "account", Pass: "secret"}}
	if c != want {
		t.Fatalf("ParseAddress = %+v, want %+v", c, want)
	}
	c, err = ParseAddress(strings.Fields("-sasl-pass secret -sasl-user account irc.gloom.im +6697"), "tester")
	if err != nil || !c.TLS || c.SASL.User != "account" {
		t.Fatalf("SASL with a +port: %+v, %v", c, err)
	}
	for args, want := range map[string]error{
		"-sasl-user account -sasl-pass secret irc.gloom.im 6697":          errSASLNeedsTLS,
		"-tls -sasl-user account irc.gloom.im":                            errSASLIncomplete,
		"-tls -sasl-pass secret irc.gloom.im":                             errSASLIncomplete,
		"-tls -sasl-mech PLAIN irc.gloom.im":                              errSASLIncomplete,
		"-tls -sasl-user account -sasl-pass secret -sasl-mech EXTERNAL h": errSASLMechanism,
	} {
		if _, err := ParseAddress(strings.Fields(args), "tester"); !errors.Is(err, want) {
			t.Errorf("ParseAddress(%q) = %v, want %v", args, err, want)
		}
	}
	for _, args := range []string{"-tls -sasl-user", "-tls -sasl-user account", "-bogus irc.gloom.im", "-tls -sasl-user a -sasl-pass b"} {
		if _, err := ParseAddress(strings.Fields(args), "tester"); err == nil || err.Error() != ConnectUsage {
			t.Errorf("ParseAddress(%q) = %v, want usage", args, err)
		}
	}
}

func TestNewSASLRejectsUnsafeCredentials(t *testing.T) {
	for _, tc := range [][2]string{{"acc\x00ount", "secret"}, {"account", "se\x00cret"}, {"account", strings.Repeat("x", 256)}, {"account", "\xff"}} {
		if _, err := NewSASL("", tc[0], tc[1]); !errors.Is(err, errSASLCredential) {
			t.Errorf("NewSASL(%q, %q) = %v", tc[0], tc[1], err)
		}
	}
	s, err := NewSASL("", "account", "pass with spaces: ok")
	if err != nil || s.Mechanism != "PLAIN" || !s.Enabled() {
		t.Fatalf("NewSASL = %+v, %v", s, err)
	}
}

func TestDialValidatesSASLBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		name string
		sasl SASL
		tls  bool
		want error
	}{
		{name: "plaintext", sasl: SASL{User: "account", Pass: "secret"}, want: errSASLNeedsTLS},
		{name: "missing account", sasl: SASL{Pass: "secret"}, tls: true, want: errSASLIncomplete},
		{name: "missing password", sasl: SASL{User: "account"}, tls: true, want: errSASLIncomplete},
		{name: "unsupported mechanism", sasl: SASL{Mechanism: "EXTERNAL", User: "account", Pass: "secret"}, tls: true, want: errSASLMechanism},
		{name: "NUL", sasl: SASL{User: "account", Pass: "bad\x00password"}, tls: true, want: errSASLCredential},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Dial(t.Context(), Config{Host: "127.0.0.1", Port: "1", Nick: "tester", TLS: tc.tls, SASL: tc.sasl})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Dial error = %v, want %v before network access", err, tc.want)
			}
		})
	}
}

func TestSASLRejectsUntrustedTLSCertificate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	fixture := httptest.NewUnstartedServer(nil)
	fixture.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	fixture.StartTLS()
	defer fixture.Close()
	host, port, err := net.SplitHostPort(fixture.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = Dial(t.Context(), Config{Host: host, Port: port, Nick: "tester", TLS: true, SASL: SASL{User: "account", Pass: "secret"}})
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); !ok {
		t.Fatalf("expected certificate verification failure, got %v", err)
	}
}

func TestSASLQueueFailureEndsNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name string
		step saslStep
		line string
	}{
		{name: "request", step: stepListing, line: ":irc.gloom.im CAP * LS :sasl"},
		{name: "mechanism", step: stepRequested, line: ":irc.gloom.im CAP * ACK :sasl"},
		{name: "payload", step: stepMechanism, line: "AUTHENTICATE +"},
		{name: "end", step: stepPayload, line: ":irc.gloom.im 903 tester :Success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{out: make(chan outgoing, 1), done: make(chan struct{})}
			c.startNegotiation(SASL{User: "account", Pass: "secret"}, saslTimeout)
			defer c.sasl.stop()
			c.sasl.step = tc.step
			c.out <- outgoing{line: "PRIVMSG #gloom :queued"}
			m, err := ParseMessage(tc.line)
			if err != nil {
				t.Fatal(err)
			}
			_, notice, err := c.handleSASL(m)
			if !errors.Is(err, ErrQueueFull) || notice != "" {
				t.Fatalf("queue failure returned notice %q, error %v", notice, err)
			}
			if c.sasl.step != stepDone || c.sasl.creds != (SASL{}) {
				t.Fatal("failed exchange retained credentials or remained active")
			}
		})
	}
}

func TestSASLPlainChunkedExchange(t *testing.T) {
	for _, size := range []int{297, 298, 510} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			// PLAIN includes two NUL bytes. A 300-byte message produces exactly
			// 400 base64 bytes and requires a final AUTHENTICATE +.
			user := strings.Repeat("u", size/2)
			pass := strings.Repeat("p", size-len(user))
			payload := base64.StdEncoding.EncodeToString([]byte("\x00" + user + "\x00" + pass))
			steps := []string{
				"< :irc.gloom.im CAP * LS :sasl=PLAIN",
				"> CAP REQ :sasl",
				"< :irc.gloom.im CAP * ACK :sasl",
				"> AUTHENTICATE PLAIN",
				"< PING :during-authentication",
				"> PONG :during-authentication",
				"< AUTHENTICATE +",
			}
			for len(payload) >= 400 {
				steps = append(steps, "> AUTHENTICATE "+payload[:400])
				payload = payload[400:]
			}
			if payload == "" {
				payload = "+"
			}
			steps = append(steps, "> AUTHENTICATE "+payload,
				"< :irc.gloom.im 903 tester :Success", "> CAP END", "< :irc.gloom.im 001 tester :Welcome")
			conf, served := converse(t, script(steps...))
			conf.SASL = SASL{User: user, Pass: pass}
			c, err := Dial(t.Context(), conf)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			untilWelcome(t, c)
			if err := <-served; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSASLTimerResumesRegistration(t *testing.T) {
	conf, served := converse(t, script(
		"> CAP END",
		"< :irc.gloom.im 001 tester :Welcome",
	))
	c, err := Dial(t.Context(), conf)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.sasl.mu.Lock()
	c.sasl.timer.Reset(20 * time.Millisecond)
	c.sasl.mu.Unlock()
	notices, _ := untilWelcome(t, c)
	if !slices.Equal(notices, []string{"SASL authentication timed out. Continuing without authentication."}) {
		t.Fatalf("timeout notices = %q", notices)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func TestSASLQueueFailureDisconnects(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	c := &Client{conn: conn, Events: make(chan Event, 4), Raw: make(chan Line, 4), out: make(chan outgoing, 1), done: make(chan struct{})}
	defer c.Close()
	c.startNegotiation(SASL{User: "account", Pass: "secret"}, saslTimeout)
	c.out <- outgoing{line: "PRIVMSG #gloom :queued"}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		c.readLoop()
	}()
	_ = peer.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprint(peer, ":irc.gloom.im CAP * LS :sasl\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("queue failure did not stop the reader")
	}
	if event := <-c.Events; !errors.Is(event.Err, ErrQueueFull) {
		t.Fatalf("queue failure event = %+v", event)
	}
	select {
	case <-c.done:
	default:
		t.Fatal("queue failure left the connection open")
	}
}
