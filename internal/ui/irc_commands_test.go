package ui

import (
	"bufio"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"gloom.im/gloom/internal/irc"
)

func TestCommonCommandsOnWire(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 128)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			received <- scanner.Text()
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	client, err := irc.Dial(t.Context(), irc.Config{Host: host, Port: port, Nick: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	read := func() string {
		t.Helper()
		select {
		case s := <-received:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for command")
			return ""
		}
	}
	read()
	read()
	m := New("tester", false)
	m.client = client
	m.selectWindow(m.ensure("#gloom"))
	m.windows[m.active].joined = true
	t.Run("names after joining", func(t *testing.T) {
		deliver(t, m, ":tester!u@gloom.im JOIN #gloom")
		w := m.current()
		before := len(w.lines)
		deliver(t, m, ":irc.gloom.im 353 tester = #gloom :tester @alice")
		deliver(t, m, ":irc.gloom.im 353 tester = #gloom :+bob %carol")
		deliver(t, m, ":irc.gloom.im 366 tester #gloom :End of names")
		if len(w.lines) != before || len(w.nicks) != 4 || w.nicks["alice"] != "@alice" || w.nicks["carol"] != "%carol" {
			t.Fatal("join names must populate the nicklist without adding messages")
		}
		m.command("/names #GLOOM")
		if got := read(); got != "NAMES #GLOOM" {
			t.Fatalf("got %q, want NAMES #GLOOM", got)
		}
		deliver(t, m, ":irc.gloom.im 353 tester = #GLOOM :tester @alice")
		deliver(t, m, ":irc.gloom.im 353 tester = #GLOOM :+bob")
		deliver(t, m, ":irc.gloom.im 366 tester #GLOOM :End of names")
		if len(w.lines) != before+2 || len(w.nicks) != 3 {
			t.Fatal("explicit names must display every reply and refresh the nicklist")
		}
		for _, l := range w.lines[before:] {
			if l.code != "353" || !strings.HasPrefix(l.text, "Names: ") {
				t.Fatalf("unexpected names output: %+v", l)
			}
		}
	})
	cases := []struct{ input, want string }{
		{"/mode", "MODE #gloom"},
		{"/mode +o alice", "MODE #gloom +o alice"},
		{"/mode #other +b *!*@gloom.im", "MODE #other +b *!*@gloom.im"},
		{"/mode tester +i", "MODE tester +i"},
		{"/invite alice", "INVITE alice #gloom"},
		{"/invite bob #other", "INVITE bob #other"},
		{"/motd", "MOTD"},
		{"/motd irc.gloom.im", "MOTD irc.gloom.im"},
		{"/away getting coffee", "AWAY :getting coffee"},
		{"/away", "AWAY"},
		{"/whois alice", "WHOIS alice"},
		{"/whois irc.gloom.im alice,bob", "WHOIS irc.gloom.im alice,bob"},
		{"/whowas alice", "WHOWAS alice"},
		{"/whowas alice 5 irc.gloom.im", "WHOWAS alice 5 irc.gloom.im"},
		{"/names", "NAMES #gloom"},
		{"/names #other", "NAMES #other"},
		{"/names #gloom,#other", "NAMES #gloom,#other"},
		{"/list", "LIST"},
		{"/list #gloom", "LIST #gloom"},
		{"/who", "WHO #gloom"},
		{"/who *.gloom.im o", "WHO *.gloom.im o"},
		{"/msg alice hello there", "PRIVMSG alice :hello there"},
		{"/privmsg alice hello again", "PRIVMSG alice :hello again"},
		{"/notice alice hello there", "NOTICE alice :hello there"},
		{"/notice #gloom channel notice", "NOTICE #gloom :channel notice"},
		{"/topic", "TOPIC #gloom"},
		{"/topic #other", "TOPIC #other"},
		{"/topic new channel topic", "TOPIC #gloom :new channel topic"},
		{"/topic #other new topic", "TOPIC #other :new topic"},
		{"/topic :", "TOPIC #gloom :"},
		{"/topic #other :", "TOPIC #other :"},
		{"/kick alice", "KICK #gloom alice"},
		{"/kick #other bob be nice", "KICK #other bob :be nice"},
		{"/leave #gloom goodbye friends", "PART #gloom :goodbye friends"},
		{"/part goodbye friends", "PART #gloom :goodbye friends"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			m.command(tc.input)
			if got := read(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	for _, input := range []string{"/invite", "/invite alice bad", "/motd a b", "/mode :bad", "/away hello\r\nQUIT", "/whois", "/whois :bad", "/whowas alice -1", "/whowas alice nope", "/names #gloom,", "/list a b c", "/notice alice", "/kick", "/kick #gloom :bad", "/notice alice hello\r\nQUIT", "/topic bad\x00topic", "/me waves", "/msg alice \x01VERSION\x01", "/privmsg alice prefix\x01ACTION waves\x01", "/notice alice \x01PING probe\x01", "\x01DCC SEND file 2130706433 1234 1\x01"} {
		m.command(input)
	}
	// A valid command after rejected input confirms no rejected command reached the wire.
	m.command("/whois sentinel")
	if got := read(); got != "WHOIS sentinel" {
		t.Fatalf("invalid command reached server: %q", got)
	}
	m.selectWindow(0)
	m.command("/mode")
	if got := read(); got != "MODE tester" {
		t.Fatal(got)
	}
	m.command("/who")
	if got := read(); got != "WHO 0" {
		t.Fatal(got)
	}
	m.command("/names")
	if got := read(); got != "NAMES" {
		t.Fatal(got)
	}
}

func TestCommandRepliesAndCompletion(t *testing.T) {
	m := New("tester", false)
	deliver(t, m, ":irc.gloom.im 311 tester alice user gloom.im * :Alice Example")
	deliver(t, m, ":irc.gloom.im 322 tester #gloom 42 :A channel")
	deliver(t, m, ":irc.gloom.im 352 tester #gloom user gloom.im irc.gloom.im alice H :0 Alice")
	for _, text := range []string{"Alice Example", "A channel", "alice H"} {
		found := false
		for _, l := range m.windows[0].lines {
			found = found || strings.Contains(l.text, text)
		}
		if !found {
			t.Fatalf("missing reply %q", text)
		}
	}
	deliver(t, m, ":irc.gloom.im 353 tester = #gloom :@alice tester")
	deliver(t, m, ":irc.gloom.im 366 tester #gloom :End of names")
	deliver(t, m, ":irc.gloom.im 332 tester #gloom :Current topic")
	w := m.windows[m.ensure("#gloom")]
	if w.topic != "Current topic" || len(w.nicks) != 2 {
		t.Fatal("channel replies missing")
	}
	deliver(t, m, ":irc.gloom.im 331 tester #gloom :No topic is set")
	if w.topic != "" {
		t.Fatal("stale topic retained")
	}
	for _, command := range []string{"/whois", "/names", "/list", "/who", "/msg", "/notice", "/topic", "/kick", "/leave", "/whowas", "/privmsg", "/clear", "/mode", "/invite", "/motd", "/away"} {
		m.input.SetValue(command[:len(command)-1])
		m.complete()
		if m.input.Value() != command+" " {
			t.Fatalf("completion for %s: %q", command, m.input.Value())
		}
	}
}

func TestClearCurrentWindow(t *testing.T) {
	m := New("tester", false)
	i := m.ensure("#gloom")
	m.windows[i].joined = true
	m.windows[i].nicks["alice"] = "@alice"
	m.add(i, "alice", "hello tester")
	m.windows[i].offset = 10
	m.selectWindow(i)
	m.command("/clear")
	w := m.windows[i]
	if len(w.lines) != 0 || w.offset != 0 || w.unread != 0 || w.mention {
		t.Fatal("current window history was not cleared")
	}
	if len(m.windows[0].lines) == 0 || !w.joined || w.nicks["alice"] != "@alice" {
		t.Fatal("clear changed other history or membership")
	}
}

// TestClearEmptiesEachWindowKind checks that /clear leaves a channel, a
// private message and the server window with no visible history.
func TestClearEmptiesEachWindowKind(t *testing.T) {
	for _, tc := range []struct{ name, window, text string }{
		{"channel", "#gloom", "channel text"},
		{"private message", "bob", "private text"},
		{"server", "irc.gloom.im", "server text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := chatModel(t)
			m.openServer("irc.gloom.im")
			deliver(t, m, ":irc.gloom.im 001 tester :Welcome server text")
			deliver(t, m, ":irc.gloom.im NOTICE tester :server text")
			deliver(t, m, ":tester!u@gloom.im JOIN #gloom")
			deliver(t, m, ":alice!u@gloom.im PRIVMSG #gloom :channel text")
			deliver(t, m, ":bob!u@gloom.im PRIVMSG tester :private text")
			i := slices.IndexFunc(m.windows, func(w *window) bool { return w.name == tc.window })
			if i < 0 {
				t.Fatalf("no %s window", tc.window)
			}
			m.selectWindow(i)
			if !strings.Contains(ansi.Strip(m.View().Content), tc.text) {
				t.Fatalf("%s window does not show %q before /clear", tc.window, tc.text)
			}
			m.command("/clear")
			screen := ansi.Strip(m.View().Content)
			if len(m.windows[i].lines) != 0 || len(m.body.rows) != 0 {
				t.Fatalf("%s window still has history after /clear", tc.window)
			}
			if strings.Contains(screen, tc.text) {
				t.Fatalf("%s window still shows %q after /clear", tc.window, tc.text)
			}
		})
	}
}

func TestQuitCommandsFlushReason(t *testing.T) {
	for _, input := range []string{"/quit", "/exit", "/quit heading out", "/exit :back later"} {
		exits := strings.HasPrefix(input, "/exit")
		t.Run(input, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			lines := make(chan []string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				scanner := bufio.NewScanner(conn)
				var got []string
				for scanner.Scan() {
					got = append(got, scanner.Text())
				}
				lines <- got
			}()
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			client, err := irc.Dial(t.Context(), irc.Config{Host: host, Port: port, Nick: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			m := New("tester", false)
			m.client = client
			m.ensure("#gloom")
			m.windows[m.find("#gloom")].lines = []line{{nick: "alice", text: "kept"}}
			if cmd := m.command("/quit bad\r\nreason"); cmd != nil || m.quitting || m.client == nil {
				t.Fatal("invalid quit reason accepted")
			}
			if err := client.Send("PRIVMSG alice :last message"); err != nil {
				t.Fatal(err)
			}
			cmd := m.command(input)
			if cmd == nil {
				t.Fatal("missing exit command")
			}
			_, quit := cmd().(tea.QuitMsg)
			if quit != exits {
				t.Fatalf("%s: exits gloom = %v, want %v", input, quit, exits)
			}
			if !exits {
				w := m.windows[m.find("#gloom")]
				if m.client != nil || len(m.windows) != 2 || len(w.lines) != 1 || w.lines[0].text != "kept" {
					t.Fatal("/quit must disconnect but keep every window and its history")
				}
			}
			_, reason, _ := strings.Cut(input, " ")
			reason = strings.TrimPrefix(reason, ":")
			want := "QUIT"
			if reason != "" {
				want += " :" + reason
			}
			select {
			case got := <-lines:
				if len(got) != 4 || got[2] != "PRIVMSG alice :last message" || got[3] != want {
					t.Fatalf("unexpected outgoing lines: %q", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("quit did not close connection")
			}
		})
	}
}

func TestNickChangeIsReportedWhereTyped(t *testing.T) {
	last := func(m *Model, i int) string {
		w := m.windows[i]
		if len(w.lines) == 0 {
			return ""
		}
		return w.lines[len(w.lines)-1].text
	}
	m := New("tester", false)
	m.selectWindow(m.ensure("#gloom"))
	m.command("/nick carol")
	if m.nick != "carol" || last(m, m.active) != "tester is now carol" {
		t.Fatalf("offline change not shown at once: %q", last(m, m.active))
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		if conn, err := listener.Accept(); err == nil {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			bufio.NewScanner(conn).Scan()
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	client, err := irc.Dial(t.Context(), irc.Config{Host: host, Port: port, Nick: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	m.client = client
	channel := m.active
	before := len(m.windows[channel].lines)
	m.command("/nick dave")
	if m.nick != "carol" || len(m.windows[channel].lines) != before {
		t.Fatal("connected change shown before the server confirmed it")
	}
	m.selectWindow(0)
	deliver(t, m, ":carol!u@gloom.im NICK dave")
	if m.nick != "dave" || last(m, channel) != "carol is now dave" || len(m.windows[0].lines) != 1 {
		t.Fatalf("confirmation not shown in the window it was typed in: %q", last(m, channel))
	}
	m.command("/nick erin")
	deliver(t, m, ":irc.gloom.im 433 dave erin :Nickname is already in use")
	deliver(t, m, ":dave!u@gloom.im NICK frank")
	if last(m, channel) != "carol is now dave" || m.nickWin != nil {
		t.Fatal("a refused change kept its window for a later one")
	}
}
