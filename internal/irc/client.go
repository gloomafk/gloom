// Package irc provides a bounded, asynchronous single-server IRC transport.
package irc

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

const (
	dialTimeout  = 10 * time.Second
	writeTimeout = 5 * time.Second
	quitTimeout  = 2 * time.Second
	maxLine      = 510 // bytes of a command line, without its CRLF
	maxNick      = 30
	maxRealname  = 100 // bytes
)

var (
	// ErrDisconnected is returned when the connection has been closed.
	ErrDisconnected = errors.New("disconnected")
	// ErrQueueFull is returned when too many lines wait to be written.
	ErrQueueFull   = errors.New("outgoing queue is full")
	errInvalidLine = errors.New("IRC line must be at most 510 bytes and contain no line breaks")
	errQuitTimeout = errors.New("quit timed out")
)

// Config is where and as whom to connect.
type Config struct {
	Host, Port, Nick string
	// Realname is sent in the USER line. When empty, the nick is sent, so the
	// server learns nothing about the client.
	Realname string
	TLS      bool
	// SASL authenticates during registration when it holds credentials.
	SASL SASL
}

// Event is something the connection reports: a message, a notice about
// authentication for the status window, or the error that ended it.
type Event struct {
	Message *Message
	Notice  string
	Err     error
}

// Line is one raw protocol line as it crossed the wire, for the debug window.
type Line struct {
	Text string
	Out  bool
}

type outgoing struct {
	line string
	// hidden keeps the line out of the debug window, for credentials.
	hidden  bool
	written chan error
}

// Client is one live connection. Events and Raw are closed once it has stopped.
type Client struct {
	conn   net.Conn
	Events chan Event
	// Raw mirrors all traffic. It is lossy: lines are dropped when nobody reads.
	Raw chan Line
	out chan outgoing
	// pong holds replies to PING. The writer drains it before out, so a full
	// queue of user lines cannot make the server time the client out.
	pong chan outgoing
	done chan struct{}
	once sync.Once
	// sasl negotiates authentication, nil when no credentials were given.
	sasl *negotiation
}

// NewConfig validates a server address. An empty port selects the default for
// the transport, and a port written "+6697" selects TLS.
func NewConfig(host, port string, useTLS bool, nick string) (Config, error) {
	c := Config{Host: host, Port: port, Nick: nick, TLS: useTLS}
	if p, ok := strings.CutPrefix(c.Port, "+"); ok {
		c.TLS = true
		c.Port = p
	}
	if c.Port == "" {
		c.Port = "6667"
		if c.TLS {
			c.Port = "6697"
		}
	}
	p, err := strconv.Atoi(c.Port)
	if err != nil || p < 1 || p > 65535 || c.Host == "" || strings.IndexFunc(c.Host, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return c, errors.New("invalid server or port")
	}
	return c, nil
}

// ConnectUsage describes the arguments of /connect.
const ConnectUsage = "usage: /connect [-TLS] [-sasl-user account -sasl-pass password [-sasl-mech PLAIN]] host [port]"

// ParseAddress reads the arguments of /connect: options, then host [port].
// The options are -TLS and the SASL credentials -sasl-user, -sasl-pass and
// -sasl-mech, in any order.
func ParseAddress(args []string, nick string) (Config, error) {
	usage := errors.New(ConnectUsage)
	useTLS := false
	var mechanism, user, pass string
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		option := strings.ToLower(args[0])
		if option == "-tls" {
			useTLS = true
			args = args[1:]
			continue
		}
		if len(args) < 2 {
			return Config{Port: "6667", Nick: nick}, usage
		}
		switch option {
		case "-sasl-user":
			user = args[1]
		case "-sasl-pass":
			pass = args[1]
		case "-sasl-mech":
			mechanism = args[1]
		default:
			return Config{Port: "6667", Nick: nick}, usage
		}
		args = args[2:]
	}
	if len(args) < 1 || len(args) > 2 {
		return Config{Port: "6667", Nick: nick}, usage
	}
	port := ""
	if len(args) == 2 {
		port = args[1]
	}
	c, err := NewConfig(args[0], port, useTLS, nick)
	if err != nil || mechanism+user+pass == "" {
		return c, err
	}
	// UseSASL changes c, so it runs before c is returned.
	err = c.UseSASL(mechanism, user, pass)
	return c, err
}

// Dial connects, verifying the server certificate when TLS is on, and starts
// registration. It returns once the transport is up.
func Dial(ctx context.Context, c Config) (*Client, error) {
	if !ValidNick(c.Nick) {
		return nil, errors.New("invalid nickname")
	}
	realname := cmp.Or(c.Realname, c.Nick)
	if !ValidRealname(realname) {
		return nil, errors.New("invalid realname")
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	dial := dialer.DialContext
	if c.TLS {
		dial = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}}).DialContext
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(c.Host, c.Port))
	if err != nil {
		return nil, err
	}
	client := &Client{conn: conn, Events: make(chan Event, 256), Raw: make(chan Line, 256), out: make(chan outgoing, 64), pong: make(chan outgoing, 8), done: make(chan struct{})}
	if c.SASL.Enabled() {
		// Set before the loops start, which read it. CAP LS goes first, so
		// the server holds registration until negotiation ends.
		client.sasl = client.newNegotiation(c.SASL)
		_ = client.Send("CAP LS 302")
	}
	var loops sync.WaitGroup
	loops.Go(client.writeLoop)
	loops.Go(client.readLoop)
	// Both loops stop once the client is closed. Closing the channels then
	// wakes any reader still waiting on them.
	go func() {
		loops.Wait()
		close(client.Events)
		close(client.Raw)
	}()
	_ = client.Send("NICK " + c.Nick)
	_ = client.Send("USER " + c.Nick + " 0 * :" + realname)
	return client, nil
}

// ValidNick reports whether s is a nickname that is safe to put on the wire.
func ValidNick(s string) bool {
	if s == "" || len(s) > maxNick {
		return false
	}
	for i, r := range s {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || strings.ContainsRune("[]\\`_^{|}", r)
		digit := r >= '0' && r <= '9' || r == '-'
		if !letter && !(i > 0 && digit) {
			return false
		}
	}
	return true
}

// ValidRealname reports whether s is safe as the trailing parameter of USER:
// non-empty, at most 100 bytes, and free of control characters and terminal
// escapes. Spaces and colons are allowed.
func ValidRealname(s string) bool {
	return s != "" && len(s) <= maxRealname && Clean(s) == s
}

// Atom reports whether s can stand as one middle parameter: no spaces, colon,
// comma, tab, line break or NUL.
func Atom(s string) bool { return s != "" && !strings.ContainsAny(s, " :\r\n\x00,\t") }

// ValidLine reports whether line fits in one protocol message and holds no
// line break or NUL, which would let text inject a second command.
func ValidLine(line string) bool {
	return len(line) <= maxLine && !strings.ContainsAny(line, "\r\n\x00")
}

// Send queues one protocol line, without its CRLF. It never blocks.
func (c *Client) Send(line string) error {
	if strings.ContainsRune(line, '\x01') {
		return errors.New("CTCP is not supported")
	}
	return c.enqueue(c.out, line)
}

// sendPong queues the reply to a PING on the reserved queue. The reply is
// protocol critical, so a full queue is an error that ends the connection
// rather than a silent drop.
func (c *Client) sendPong(token string) error { return c.enqueue(c.pong, "PONG :"+token) }

func (c *Client) enqueue(q chan<- outgoing, line string) error {
	return c.queue(q, outgoing{line: line})
}

func (c *Client) queue(q chan<- outgoing, message outgoing) error {
	if !ValidLine(message.line) {
		return errInvalidLine
	}
	// A closed client must win over a queue with room, which select alone would not.
	select {
	case <-c.done:
		return ErrDisconnected
	default:
	}
	select {
	case q <- message:
		return nil
	case <-c.done:
		return ErrDisconnected
	default:
		return ErrQueueFull
	}
}

// Quit flushes preceding queued lines and the quit message, then closes.
// A bounded wait prevents an unresponsive server from delaying terminal exit.
func (c *Client) Quit(reason string) error {
	line := "QUIT"
	if reason != "" {
		line += " :" + reason
	}
	if !ValidLine(line) {
		return errors.New("invalid quit reason: maximum 510-byte IRC line, no line breaks")
	}
	defer c.Close()
	timer := time.NewTimer(quitTimeout)
	defer timer.Stop()
	written := make(chan error, 1)
	select {
	case c.out <- outgoing{line: line, written: written}:
	case <-c.done:
		return ErrDisconnected
	case <-timer.C:
		return errQuitTimeout
	}
	select {
	case err := <-written:
		return err
	case <-c.done:
		return ErrDisconnected
	case <-timer.C:
		return errQuitTimeout
	}
}

// Close stops the connection. It is safe to call more than once.
func (c *Client) Close() { c.once.Do(func() { close(c.done); _ = c.conn.Close() }) }

func (c *Client) tap(l Line) {
	select {
	case c.Raw <- l:
	default:
	}
}

func (c *Client) emit(e Event) {
	select {
	case c.Events <- e:
	case <-c.done:
	}
}

func (c *Client) writeLoop() {
	for {
		var message outgoing
		select {
		case <-c.done:
			return
		case message = <-c.pong:
		default:
			select {
			case <-c.done:
				return
			case message = <-c.pong:
			case message = <-c.out:
			}
		}
		err := c.writeLine(message.line)
		if err == nil {
			shown := message.line
			if message.hidden {
				shown = hiddenPayload
			}
			c.tap(Line{Text: shown, Out: true})
		}
		if message.written != nil {
			message.written <- err
		}
		if err != nil {
			c.emit(Event{Err: err})
			c.Close()
			return
		}
	}
}

// writeLine writes one complete IRC line within the connection's write timeout.
func (c *Client) writeLine(line string) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	wire := line + "\r\n"
	n, err := io.WriteString(c.conn, wire)
	if err != nil {
		return err
	}
	if n != len(wire) {
		return io.ErrShortWrite
	}
	return nil
}

func (c *Client) readLoop() {
	if c.sasl != nil {
		// Runs after Close, which unblocks a timeout notice being emitted.
		defer c.sasl.stop()
	}
	defer c.Close()
	s := bufio.NewScanner(c.conn)
	s.Buffer(make([]byte, 4096), 8192)
	for s.Scan() {
		text := s.Text()
		m, err := ParseMessage(text)
		if err != nil {
			continue
		}
		c.tap(Line{Text: text})
		if m.Command == "PING" {
			if err := c.sendPong(m.Trailing()); err != nil {
				c.emit(Event{Err: err})
				return
			}
			continue
		}
		if c.sasl != nil {
			consumed, notice := c.handleSASL(m)
			if notice != "" {
				c.emit(Event{Notice: notice})
			}
			if consumed {
				continue
			}
		}
		c.emit(Event{Message: m})
	}
	err := s.Err()
	if err == nil {
		err = errors.New("server closed the connection")
	}
	c.emit(Event{Err: err})
}

// Clean removes terminal escapes, IRC formatting, and other control characters.
func Clean(s string) string {
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 {
			return -1
		}
		return r
	}, s)
}

// rfc1459 maps the characters that RFC 1459 treats as upper case to lower case.
var rfc1459 = strings.NewReplacer("[", "{", "]", "}", "\\", "|", "^", "~")

// Fold lower-cases s the way IRC compares nicknames and channel names.
func Fold(s string) string { return rfc1459.Replace(strings.ToLower(s)) }
