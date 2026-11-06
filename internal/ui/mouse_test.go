package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func clickAt(m *Model, x, y int, button tea.MouseButton) {
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: button})
}

// tapAt is a whole click: press, then release where the pointer is.
func tapAt(m *Model, x, y int) {
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func visibleNick(t *testing.T, m *Model, label string, sidebar bool) (int, int) {
	t.Helper()
	rows := strings.Split(m.View().Content, "\n")
	for y, row := range rows {
		if y == 0 || y >= m.height-bottomRows-1-inputPadRows {
			continue
		}
		plain := ansi.Strip(row)
		start := 0
		if sidebar {
			start = m.width - nicklistW
		}
		if start >= len(plain) {
			continue
		}
		at := strings.Index(plain[start:], label)
		if at < 0 {
			continue
		}
		x := ansi.StringWidth(plain[:start+at])
		if !sidebar && m.nicklist && m.width >= 65 && x >= m.width-nicklistW {
			continue
		}
		return x, y
	}
	t.Fatalf("visible nickname %q not found", label)
	return 0, 0
}

func TestNickClicksOpenQuery(t *testing.T) {
	cases := []struct {
		name, label, nick                  string
		sidebar, light, timestamps, scroll bool
		width                              int
	}{
		{name: "nicklist operator", label: "Alice", nick: "Alice", sidebar: true, timestamps: true, width: 100},
		{name: "nicklist voiced light", label: "bob", nick: "bob", sidebar: true, light: true, width: 100},
		{name: "message operator", label: "Alice", nick: "Alice", timestamps: true, width: 100},
		{name: "message without timestamps", label: "Alice", nick: "Alice", width: 100},
		{name: "narrow message", label: "Alice", nick: "Alice", timestamps: true, width: 40},
		{name: "truncated message nick", label: "abcdefghijk", nick: "abcdefghijklmnop", width: 100},
		{name: "scrolled wrapped messages", label: "Alice", nick: "Alice", timestamps: true, scroll: true, width: 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New("tester", tc.light)
			m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 16})
			m.timestamps = tc.timestamps
			m.selectWindow(m.ensure("#gloom"))
			w := m.windows[m.active]
			w.nicks["alice"] = "@Alice"
			w.nicks["bob"] = "+bob"
			if tc.scroll {
				for range 10 {
					m.add(m.active, "oldnick", strings.Repeat("older text ", 20))
				}
			}
			m.add(m.active, tc.nick, strings.Repeat("wrapping body ", 15))
			if tc.scroll {
				for range 10 {
					m.add(m.active, "recent", "newer message")
				}
				w.offset = 10
			}
			if !tc.scroll {
				w.offset = 99999
			}
			x, y := visibleNick(t, m, tc.label, tc.sidebar)
			tapAt(m, x, y)
			if m.windows[m.active].name != tc.nick {
				t.Fatalf("selected %q, want %q", m.windows[m.active].name, tc.nick)
			}
			if len(m.windows) != 3 {
				t.Fatal("query window missing")
			}
			m.View()
			for _, h := range m.nickHits {
				if h.start >= tc.width-nicklistW && tc.width >= 65 {
					t.Fatal("query still has nicklist targets")
				}
			}
		})
	}
}

func TestNickClickReusesQuery(t *testing.T) {
	m := New("tester", false)
	m.selectWindow(m.ensure("#gloom"))
	channel := m.active
	m.windows[channel].nicks["alice"] = "@Alice"
	query := m.ensure("ALICE")
	m.add(query, "Alice", "hello tester")
	x, y := visibleNick(t, m, "Alice", true)
	tapAt(m, x, y)
	if m.active != query || len(m.windows) != 3 {
		t.Fatal("existing query not selected")
	}
	if m.windows[query].unread != 0 || m.windows[query].mention {
		t.Fatal("query activity not cleared")
	}
}

func TestNickClickIgnoresNonTargets(t *testing.T) {
	for _, tc := range []struct {
		name           string
		x, y           int
		button         tea.MouseButton
		hidden, resize bool
	}{
		{name: "header", x: 40, y: 0, button: tea.MouseLeft},
		{name: "nicklist title", x: 82, y: 0, button: tea.MouseLeft},
		{name: "empty nicklist row", x: 82, y: 10, button: tea.MouseLeft},
		{name: "right click", x: 82, y: 1, button: tea.MouseRight},
		{name: "hidden nicklist", x: 82, y: 1, button: tea.MouseLeft, hidden: true},
		{name: "resized before redraw", x: 82, y: 1, button: tea.MouseLeft, resize: true},
		{name: "message text", x: 40, y: 1, button: tea.MouseLeft},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New("tester", false)
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
			m.selectWindow(m.ensure("#gloom"))
			m.windows[m.active].nicks["alice"] = "@Alice"
			m.add(m.active, "Alice", strings.Repeat("message ", 30))
			m.nicklist = !tc.hidden
			m.View()
			if tc.resize {
				m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
			}
			clickAt(m, tc.x, tc.y, tc.button)
			if len(m.windows) != 2 || m.active != 1 {
				t.Fatal("non-nickname click opened query")
			}
		})
	}
}

func wheel(m *Model, button tea.MouseButton, notches int) {
	for range notches {
		m.Update(tea.MouseWheelMsg{X: 20, Y: 5, Button: button})
	}
}

func TestMouseWheelScrollsWindows(t *testing.T) {
	m := New("tester", false)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	m.selectWindow(m.ensure("#gloom"))
	for i := range 60 {
		m.add(m.active, "alice", "line "+strings.Repeat("x", i))
		m.logRaw('<', "PRIVMSG #gloom :line")
	}
	chat := m.windows[m.active]
	if chat.offset != 0 {
		t.Fatalf("offset = %d before scrolling", chat.offset)
	}
	wheel(m, tea.MouseWheelUp, 2)
	if chat.offset != 2*wheelRows {
		t.Fatalf("chat offset after wheel up = %d, want %d", chat.offset, 2*wheelRows)
	}
	wheel(m, tea.MouseWheelDown, 1)
	if chat.offset != wheelRows {
		t.Fatalf("chat offset after wheel down = %d, want %d", chat.offset, wheelRows)
	}
	wheel(m, tea.MouseWheelDown, 10)
	if chat.offset != 0 {
		t.Fatalf("chat offset below zero: %d", chat.offset)
	}

	m.toggleDebug()
	raw := m.windows[m.rawWindow()]
	wheel(m, tea.MouseWheelUp, 3)
	if raw.offset != 3*wheelRows {
		t.Fatalf("debug offset after wheel up = %d, want %d", raw.offset, 3*wheelRows)
	}
	m.logRaw('<', "PING :x")
	if raw.offset != 3*wheelRows+1 {
		t.Fatalf("debug offset moved to %d on new traffic, want %d", raw.offset, 3*wheelRows+1)
	}
	if chat.offset != 0 {
		t.Fatal("scrolling debug moved the channel window")
	}

	m.view = viewConfig
	m.panelOff = 5
	wheel(m, tea.MouseWheelUp, 1)
	if m.panelOff != 5-wheelRows {
		t.Fatalf("panel offset after wheel up = %d, want %d", m.panelOff, 5-wheelRows)
	}
	wheel(m, tea.MouseWheelDown, 1)
	if m.panelOff != 5 {
		t.Fatalf("panel offset after wheel down = %d, want 5", m.panelOff)
	}
}

func TestNicklistClickUnusualNick(t *testing.T) {
	for _, nick := range []string{"4chan", "Zoë"} {
		m := New("tester", false)
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
		m.selectWindow(m.ensure("#gloom"))
		m.windows[m.active].nicks[strings.ToLower(nick)] = nick
		x, y := visibleNick(t, m, nick, true)
		tapAt(m, x, y)
		if m.windows[m.active].name != nick {
			t.Fatalf("selected %q, want %q", m.windows[m.active].name, nick)
		}
	}
}

func TestNicklistWholeRowOpensQuery(t *testing.T) {
	m := New("tester", false)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.selectWindow(m.ensure("#gloom"))
	m.windows[m.active].nicks["alice"] = "@Alice"
	m.View()
	if m.paneR == 0 {
		t.Fatal("nicklist not shown")
	}
	// The nicklist starts on the top row, so row 1 is the first nick, below
	// the NICKS title. The last column is well past the end of "Alice".
	clickAt(m, m.width-1, 1, tea.MouseLeft)
	if w := m.windows[m.active]; w.name != "Alice" {
		t.Fatalf("clicking the end of the nick row selected %q, want a query with Alice", w.name)
	}
}

func TestArrowPointerRequested(t *testing.T) {
	msg := arrowPointer()()
	if got := fmt.Sprint(msg); !strings.Contains(got, "\x1b]22;default") {
		t.Fatalf("pointer command sends %q, want an OSC 22 request for the default arrow", got)
	}
}
