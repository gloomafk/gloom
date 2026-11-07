package irc

import (
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// saslTimeout bounds the whole capability negotiation, so a server that
	// stops answering cannot hold registration open.
	saslTimeout = 20 * time.Second
	// saslChunk is the longest AUTHENTICATE payload line the protocol allows.
	saslChunk = 400
	// maxSASLField bounds the account and the password, in bytes.
	maxSASLField = 255
	// mechanismPlain is the only SASL mechanism gloom offers.
	mechanismPlain = "PLAIN"
	// hiddenPayload replaces a credentials line in the debug window.
	hiddenPayload = "AUTHENTICATE (credentials hidden)"
)

var (
	errSASLIncomplete = errors.New("SASL needs both an account and a password")
	errSASLMechanism  = errors.New("unsupported SASL mechanism: only PLAIN is available")
	errSASLCredential = errors.New("invalid SASL account or password: at most 255 bytes of UTF-8 without NUL")
	errSASLNeedsTLS   = errors.New("SASL sends the password, so it needs TLS: add -tls")
)

// SASL holds the credentials for SASL authentication during registration.
// The zero value means no authentication.
type SASL struct {
	Mechanism, User, Pass string
}

// Enabled reports whether credentials are present.
func (s SASL) Enabled() bool { return s.User != "" }

// NewSASL validates credentials. An empty mechanism selects PLAIN, the only
// one supported.
func NewSASL(mechanism, user, pass string) (SASL, error) {
	if user == "" || pass == "" {
		return SASL{}, errSASLIncomplete
	}
	if mechanism != "" && !strings.EqualFold(mechanism, mechanismPlain) {
		return SASL{}, errSASLMechanism
	}
	if !validSASLField(user) || !validSASLField(pass) {
		return SASL{}, errSASLCredential
	}
	return SASL{Mechanism: mechanismPlain, User: user, Pass: pass}, nil
}

func validSASLField(s string) bool {
	return len(s) <= maxSASLField && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// UseSASL validates credentials and attaches them to the connection. PLAIN
// reveals the password to anyone on the path, so TLS is required.
func (c *Config) UseSASL(mechanism, user, pass string) error {
	s, err := NewSASL(mechanism, user, pass)
	if err != nil {
		return err
	}
	if !c.TLS {
		return errSASLNeedsTLS
	}
	c.SASL = s
	return nil
}

// plainPayload is the base64 PLAIN response: an empty authorization
// identity, the account and the password, separated by NUL.
func plainPayload(s SASL) string {
	return base64.StdEncoding.EncodeToString([]byte("\x00" + s.User + "\x00" + s.Pass))
}

// authenticateLines splits a payload into AUTHENTICATE lines of at most 400
// bytes. A payload that ends on a full line is followed by "+", so the server
// knows it is complete.
func authenticateLines(payload string) []string {
	var lines []string
	for len(payload) >= saslChunk {
		lines = append(lines, "AUTHENTICATE "+payload[:saslChunk])
		payload = payload[saslChunk:]
	}
	if payload == "" {
		payload = "+"
	}
	return append(lines, "AUTHENTICATE "+payload)
}

type saslStep int

const (
	stepListing   saslStep = iota // sent CAP LS, reading the capability list
	stepRequested                 // sent CAP REQ :sasl
	stepMechanism                 // sent AUTHENTICATE PLAIN
	stepPayload                   // sent the credentials
	stepDone                      // sent CAP END, or the server has no CAP
)

// negotiation runs capability negotiation and SASL for one connection. It
// holds the credentials only until the exchange ends.
type negotiation struct {
	mu         sync.Mutex
	step       saslStep
	creds      SASL
	listed     bool   // the server offers sasl
	mechanisms string // the mechanisms in the sasl capability value, if any
	err        error
	timer      *time.Timer
}

// saslReplies are the numerics that end or explain a SASL exchange.
const (
	errNickLocked   = "902"
	rplSASLSuccess  = "903"
	errSASLFail     = "904"
	errSASLTooLong  = "905"
	errSASLAborted  = "906"
	errSASLAlready  = "907"
	rplSASLMechs    = "908"
	errUnknownCmd   = "421"
	rplWelcome      = "001"
	continueWithout = " Continuing without authentication."
)

// startNegotiation attaches negotiation to c and arms its timeout. It must
// run before the read loop starts. Registration waits on the server until
// CAP END.
func (c *Client) startNegotiation(creds SASL, timeout time.Duration) {
	n := &negotiation{creds: creds}
	// The timer's callback reads c.sasl, so it is set before the timer is
	// armed. The lock orders the timer field before the callback reads it.
	c.sasl = n
	n.mu.Lock()
	defer n.mu.Unlock()
	n.timer = time.AfterFunc(timeout, c.saslTimedOut)
}

// end stops negotiation and forgets the credentials. The caller holds the lock.
func (n *negotiation) end() {
	n.step = stepDone
	n.timer.Stop()
	n.creds = SASL{}
}

// saslTimedOut ends a negotiation the server did not finish in time. It
// runs on the timer's goroutine, outside the loops that guard Events, so it
// emits while holding the lock: the read loop's stop waits for it, and
// Events is closed only after that.
func (c *Client) saslTimedOut() {
	n := c.sasl
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.step == stepDone {
		return
	}
	if n.step == stepMechanism || n.step == stepPayload {
		c.queueSASL(outgoing{line: "AUTHENTICATE *"})
	}
	notice := c.finishSASL("SASL authentication timed out." + continueWithout)
	c.emit(Event{Notice: notice, Err: n.err})
	if n.err != nil {
		c.Close()
	}
}

// stop releases the timer and the credentials when the connection ends.
func (n *negotiation) stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.end()
}

// finishSASL ends negotiation with CAP END and returns notice for the
// status window. The caller holds the lock.
func (c *Client) finishSASL(notice string) string {
	c.sasl.end()
	if !c.queueSASL(outgoing{line: "CAP END"}) {
		return ""
	}
	return notice
}

// queueSASL records failures of protocol-critical writes. The caller holds
// the negotiation lock and reports the error before ending the connection.
func (c *Client) queueSASL(message outgoing) bool {
	if c.sasl.err != nil {
		return false
	}
	if err := c.queue(c.out, message); err != nil {
		c.sasl.err = err
		c.sasl.end()
		return false
	}
	return true
}

// handleSASL processes one message during negotiation. It reports whether
// the message was consumed, and returns a notice for the status window.
func (c *Client) handleSASL(m *Message) (bool, string, error) {
	n := c.sasl
	n.mu.Lock()
	defer n.mu.Unlock()
	consumed, notice := c.negotiateSASL(m)
	return consumed, notice, n.err
}

// negotiateSASL advances the exchange while the caller holds its lock.
func (c *Client) negotiateSASL(m *Message) (consumed bool, notice string) {
	n := c.sasl
	switch m.Command {
	case "CAP":
		// After negotiation, CAP NEW and CAP DEL from cap-notify are not
		// shown: gloom requests nothing more.
		if n.step != stepDone && len(m.Params) >= 3 {
			notice = c.saslCap(strings.ToUpper(m.Params[1]), m.Params[2:])
		}
		return true, notice
	case "AUTHENTICATE":
		if n.step != stepMechanism || len(m.Params) < 1 || m.Params[0] != "+" {
			return true, ""
		}
		n.step = stepPayload
		for _, line := range authenticateLines(plainPayload(n.creds)) {
			if !c.queueSASL(outgoing{line: line, hidden: true}) {
				break
			}
		}
		return true, ""
	}
	if n.step == stepDone {
		return false, ""
	}
	switch m.Command {
	case rplSASLSuccess:
		return true, c.finishSASL("SASL authentication succeeded.")
	case errSASLAlready:
		return true, c.finishSASL("Already authenticated with SASL.")
	case errSASLFail:
		return true, c.finishSASL("SASL authentication failed: the server rejected the account or password." + continueWithout)
	case errSASLTooLong:
		return true, c.finishSASL("SASL authentication failed: the credentials are too long for the server." + continueWithout)
	case errSASLAborted:
		return true, c.finishSASL("SASL authentication was aborted." + continueWithout)
	case errNickLocked:
		return true, c.finishSASL("SASL authentication failed: " + Clean(m.Trailing()) + "." + continueWithout)
	case rplSASLMechs:
		// 904 follows with the failure.
		return true, ""
	case errUnknownCmd:
		if len(m.Params) < 2 || !strings.EqualFold(m.Params[1], "CAP") {
			return false, ""
		}
		// The server registers without CAP, so CAP END would be unknown too.
		n.end()
		return true, "The server does not support capability negotiation." + continueWithout
	case rplWelcome:
		n.end()
		return false, "The server registered the connection before SASL finished." + continueWithout
	}
	return false, ""
}

// saslCap handles one CAP reply: LS, ACK or NAK. The caller holds the lock.
func (c *Client) saslCap(sub string, params []string) string {
	n := c.sasl
	switch sub {
	case "LS":
		if n.step != stepListing {
			return ""
		}
		more := len(params) > 1 && params[0] == "*"
		for capability := range strings.FieldsSeq(params[len(params)-1]) {
			name, value, _ := strings.Cut(capability, "=")
			if strings.EqualFold(name, "sasl") {
				n.listed, n.mechanisms = true, value
			}
		}
		if more {
			return ""
		}
		if !n.listed {
			return c.finishSASL("The server does not offer SASL." + continueWithout)
		}
		if n.mechanisms != "" && !offersPlain(n.mechanisms) {
			return c.finishSASL("The server does not offer SASL PLAIN, only " + Clean(n.mechanisms) + "." + continueWithout)
		}
		n.step = stepRequested
		c.queueSASL(outgoing{line: "CAP REQ :sasl"})
	case "ACK":
		if n.step != stepRequested {
			return ""
		}
		// gloom requests only sasl, so an ACK without it leaves nothing to
		// wait for.
		if !capListed(params[len(params)-1], "sasl") {
			return c.finishSASL("The server did not acknowledge the SASL capability." + continueWithout)
		}
		n.step = stepMechanism
		c.queueSASL(outgoing{line: "AUTHENTICATE " + mechanismPlain})
	case "NAK":
		if n.step != stepRequested {
			return ""
		}
		return c.finishSASL("The server refused the SASL capability." + continueWithout)
	}
	return ""
}

func offersPlain(mechanisms string) bool {
	for mechanism := range strings.SplitSeq(mechanisms, ",") {
		if strings.EqualFold(mechanism, mechanismPlain) {
			return true
		}
	}
	return false
}

func capListed(list, name string) bool {
	for capability := range strings.FieldsSeq(list) {
		if strings.EqualFold(capability, name) {
			return true
		}
	}
	return false
}
