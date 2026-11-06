package ui

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"gloom.im/gloom/internal/irc"
)

func deliver(t *testing.T, m *Model, s string) {
	t.Helper()
	p, err := irc.ParseMessage(s)
	if err != nil {
		t.Fatal(err)
	}
	m.receive(irc.Event{Message: p})
}
func TestWindowsAndMembership(t *testing.T) {
	m := New("tester", false)
	deliver(t, m, ":tester!u@gloom.im JOIN #gloom")
	deliver(t, m, ":irc.gloom.im 353 tester = #gloom :@alice +bob tester")
	if !m.windows[1].joined || len(m.windows[1].nicks) != 3 {
		t.Fatal("membership not tracked")
	}
	m.selectWindow(0)
	deliver(t, m, ":alice!u@gloom.im PRIVMSG #gloom :hello tester")
	if m.windows[1].unread != 1 || !m.windows[1].mention {
		t.Fatal("activity not tracked")
	}
	deliver(t, m, ":alice!u@gloom.im NICK carol")
	if m.windows[1].nicks["carol"] != "@carol" {
		t.Fatal("nickname mode not preserved")
	}
	deliver(t, m, ":bob!u@gloom.im QUIT :gone")
	if _, ok := m.windows[1].nicks["bob"]; ok {
		t.Fatal("quit nick retained")
	}
	m.selectWindow(1)
	if m.windows[1].unread != 0 || m.windows[1].mention {
		t.Fatal("activity not reset")
	}
	deliver(t, m, ":op!u@gloom.im KICK #gloom tester :bye")
	if m.windows[1].joined {
		t.Fatal("kick not tracked")
	}
}
func TestBoundedScrollbackAndLayout(t *testing.T) {
	m := New("tester", false)
	m.ensure("#gloom")
	m.selectWindow(1)
	for range 2100 {
		m.add(1, "friend", "a long message with Unicode: 你好 🌙 "+strings.Repeat("word ", 40))
	}
	if len(m.windows[1].lines) != 2000 {
		t.Fatal("scrollback is unbounded")
	}
	for _, size := range [][2]int{{100, 30}, {40, 10}, {30, 6}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.windows[1].offset = 999999
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != size[1] {
			t.Fatalf("height: got %d want %d", len(lines), size[1])
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != size[0] {
				t.Fatalf("width: got %d want %d", ansi.StringWidth(line), size[0])
			}
		}
	}
}

var reverseVideo = regexp.MustCompile(`\x1b\[(?:[0-9;]*;)?7(?:;[0-9;]*)?m`)

// inputCursorShown reports whether the input row draws the block cursor, which
// is reverse video.
func inputCursorShown(m *Model) bool {
	for row := range strings.SplitSeq(m.View().Content, "\n") {
		if strings.Contains(row, "❯") {
			return reverseVideo.MatchString(row)
		}
	}
	return false
}

func TestInputCursorBlinks(t *testing.T) {
	m := New("tester", false)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	_, cmd := m.Update(textinput.Blink())
	if !inputCursorShown(m) {
		t.Fatal("cursor hidden at start")
	}
	var shown, hidden int
	for range 4 {
		start := time.Now()
		_, cmd = m.Update(cmd())
		if d := time.Since(start); d < cursorBlink-50*time.Millisecond || d > cursorBlink+250*time.Millisecond {
			t.Fatalf("blink period %v, want about %v", d, cursorBlink)
		}
		// The clock tick renders between blinks and must not reset the cursor.
		m.Update(tick(time.Now()))
		if inputCursorShown(m) {
			shown++
		} else {
			hidden++
		}
	}
	if shown != 2 || hidden != 2 {
		t.Fatalf("cursor shown %d times and hidden %d times over 4 blinks, want 2 and 2", shown, hidden)
	}
}

func TestConnectOpensServerWindow(t *testing.T) {
	m := New("tester", false)
	defer m.Close()
	if len(m.windows) != 1 || m.windows[0].name != "gloom" {
		t.Fatal("gloom must be the only window before connecting")
	}
	if strings.Contains(ansi.Strip(statusBar(m)[statusTextRow]), "gloom") {
		t.Fatal("the logo button is window 0 and must not also be a tab")
	}
	m.connect(irc.Config{Host: "irc.gloom.im", Port: "6667", Nick: "tester"})
	if m.find("irc.gloom.im") != 1 || !m.windows[1].server || m.active != 1 {
		t.Fatal("server window not opened and selected")
	}
	deliver(t, m, ":irc.gloom.im 001 tester :Welcome")
	deliver(t, m, ":tester!u@gloom.im JOIN #gloom")
	if len(m.windows[1].lines) < 2 || len(m.windows[0].lines) != 1 {
		t.Fatal("server notices must land in the server window")
	}
	if m.find("#gloom") != 2 {
		t.Fatal("channels must follow the server window")
	}
	m.selectWindow(1)
	m.command("hello")
	if got := m.windows[1].lines[len(m.windows[1].lines)-1].text; got != "Unknown command: hello. Type /help." {
		t.Fatalf("plain text in the server window was not refused: %q", got)
	}
}

func TestReconnectReusesServerWindow(t *testing.T) {
	m := New("tester", false)
	defer m.Close()
	m.connect(irc.Config{Host: "irc.gloom.im", Port: "6667", Nick: "tester"})
	deliver(t, m, ":irc.gloom.im 001 tester :Welcome")
	deliver(t, m, ":tester!u@gloom.im JOIN #gloom")
	m.disconnect()
	m.connect(irc.Config{Host: "irc2.gloom.im", Port: "6667", Nick: "tester"})
	if len(m.windows) != 3 || m.find("irc.gloom.im") >= 0 || m.find("irc2.gloom.im") != 1 || m.active != 1 {
		t.Fatalf("server window not reused and renamed: %d windows", len(m.windows))
	}
	var texts []string
	for _, l := range m.windows[1].lines {
		texts = append(texts, l.text)
	}
	if !slices.Contains(texts, "Welcome") || !strings.HasPrefix(texts[len(texts)-1], "Connecting to irc2.gloom.im") {
		t.Fatalf("old contents lost or new attempt missing: %q", texts)
	}
}

func TestNumericRepliesDropTargetAndShowCode(t *testing.T) {
	m := sized(100, 24)
	m.selectWindow(0)
	deliver(t, m, ":irc.gloom.im 251 tester :There are 102 users")
	deliver(t, m, ":irc.gloom.im 372 tester :- Message of the Day -")
	deliver(t, m, ":irc.gloom.im FOO tester extra")
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"251 ▏ There are 102 users", "372 ▏ - Message of the Day -", "← ▏ tester extra"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from:\n%s", want, out)
		}
	}
}

func TestClientNotesUseSentArrowAndServerLinesUseReceivedArrow(t *testing.T) {
	m := sized(100, 24)
	m.selectWindow(0)
	m.command("/bogus")
	deliver(t, m, "NOTICE AUTH :*** Looking up your hostname")
	deliver(t, m, ":irc.gloom.im FOO tester extra")
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"→ ▏ Unknown command:", "← ▏ *** Looking up your hostname", "← ▏ tester extra"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from:\n%s", want, out)
		}
	}
	if strings.Contains(out, "! ▏") {
		t.Fatalf("a line still uses the old \"!\" glyph:\n%s", out)
	}
}

func TestServerNoticesGoToServerWindow(t *testing.T) {
	m := New("tester", false)
	defer m.Close()
	m.connect(irc.Config{Host: "irc.gloom.im", Port: "6667", Nick: "tester"})
	m.selectWindow(0)
	deliver(t, m, ":irc.gloom.im NOTICE * :*** Looking up your hostname...")
	deliver(t, m, "NOTICE AUTH :*** Checking Ident")
	deliver(t, m, ":irc.gloom.im NOTICE tester :*** No Ident response")
	if len(m.windows[0].lines) != 1 || len(m.windows[1].lines) != 4 {
		t.Fatalf("notices leaked into the gloom window: %d lines there, %d in the server window", len(m.windows[0].lines), len(m.windows[1].lines))
	}
}

func TestLeavingAChannelClosesItsWindow(t *testing.T) {
	m := New("tester", false)
	deliver(t, m, ":tester!u@gloom.im JOIN #one")
	deliver(t, m, ":tester!u@gloom.im JOIN #two")
	m.selectWindow(m.find("#two"))
	deliver(t, m, ":tester!u@gloom.im PART #one")
	if m.find("#one") >= 0 || m.windows[m.active].name != "#two" {
		t.Fatal("leaving #one must close it and keep #two selected")
	}
	deliver(t, m, ":alice!u@gloom.im JOIN #two")
	deliver(t, m, ":alice!u@gloom.im PART #two")
	deliver(t, m, ":bob!u@gloom.im JOIN #two")
	deliver(t, m, ":bob!u@gloom.im PART #two :bye all")
	var texts []string
	for _, l := range m.windows[m.find("#two")].lines {
		texts = append(texts, l.text)
	}
	if !slices.Contains(texts, "alice left") || !slices.Contains(texts, "bob left: bye all") {
		t.Fatalf("others leaving: %q", texts)
	}
	deliver(t, m, ":tester!u@gloom.im PART #two :done")
	if len(m.windows) != 1 || m.active != 0 {
		t.Fatalf("expected only the gloom window, got %d", len(m.windows))
	}
}

func TestServerNamesAreStrippedOfControlCharacters(t *testing.T) {
	m := New("tester", false)
	deliver(t, m, ":ev\x1b[31mil!u@gloom.im PRIVMSG tester :hi")
	deliver(t, m, ":tester!u@gloom.im JOIN #go\x1b]0;x\x07")
	deliver(t, m, ":irc.gloom.im 001 te\x1b[31mster :Welcome")
	for _, w := range m.windows {
		if w.name != irc.Clean(w.name) {
			t.Fatalf("window name carries control characters: %q", w.name)
		}
		for _, n := range w.nicks {
			if n != irc.Clean(n) {
				t.Fatalf("nick carries control characters: %q", n)
			}
		}
	}
	if m.nick != "tester" {
		t.Fatalf("own nick not cleaned: %q", m.nick)
	}
}

func TestIncomingCTCPDoesNotCreateMessagesWindowsOrActivity(t *testing.T) {
	for _, command := range []string{"PRIVMSG", "NOTICE"} {
		for _, target := range []string{"tester", "#gloom", "#other"} {
			for _, payload := range []string{
				"\x01ACTION waves\x01",
				"\x01VERSION\x01",
				"\x01PING probe\x01",
				"\x01DCC SEND file 2130706433 1234 1\x01",
				"\x01VERSION",
				"hello \x01VERSION\x01",
			} {
				t.Run(command+"/"+target+"/"+payload, func(t *testing.T) {
					m := New("tester", false)
					m.command("/clear")
					m.ensure("#gloom")
					deliver(t, m, ":friend!u@gloom.im "+command+" "+target+" :"+payload)
					if len(m.windows) != 2 || len(m.autoOpens) != 0 {
						t.Fatal("CTCP opened a window or consumed its allowance")
					}
					for _, w := range m.windows {
						if len(w.lines) != 0 || w.unread != 0 || w.mention {
							t.Fatal("CTCP recorded a message or activity")
						}
					}
					deliver(t, m, ":friend!u@gloom.im "+command+" #gloom :hello tester")
					if len(m.windows[1].lines) != 1 || m.windows[1].unread != 1 {
						t.Fatal("ordinary chat was not recorded")
					}
				})
			}
		}
	}
}
