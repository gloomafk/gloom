package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func nickScrollModel(t *testing.T, count int) *Model {
	t.Helper()
	m := New("tester", false)
	m.gfx = gfxNone
	m.width, m.height = 120, 20
	deliver(t, m, ":tester!u@gloom.im JOIN #gloom")
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("user%04d", i)
	}
	deliver(t, m, ":irc.gloom.im 353 tester = #gloom :"+strings.Join(names, " "))
	deliver(t, m, ":irc.gloom.im 366 tester #gloom :End of /NAMES list.")
	for range 40 {
		m.add(m.active, "user0000", "chat history")
	}
	m.View()
	return m
}

func visibleMember(t *testing.T, m *Model, name string) (int, int) {
	t.Helper()
	rows := strings.Split(m.View().Content, "\n")
	for y := 1; y < m.height-statusRows; y++ {
		row := ansi.Cut(rows[y], m.width-m.paneR, m.width)
		if strings.Contains(ansi.Strip(row), name) {
			return m.width - 2, y
		}
	}
	t.Fatalf("visible member %q not found", name)
	return 0, 0
}

// Acceptance coverage:
// ```gherkin
// Scenario: Reach every channel member
//
//	Given more channel members than fit in the nicklist
//	When I scroll down over the nicklist
//	Then every member can be reached and clicked
//	And the title and chat history stay in place
//
// ```
func TestNicklistScrollReachesEveryMember(t *testing.T) {
	for _, light := range []bool{false, true} {
		m := nickScrollModel(t, 70)
		m.light = light
		m.channels = true
		m.View()
		w := m.current()
		seen := map[string]bool{}
		header, _, _ := strings.Cut(m.View().Content, "\n")
		for range 30 {
			for _, h := range m.nickHits {
				if h.start > m.width-m.paneR {
					seen[h.nick] = true
				}
			}
			wheelAt(m, m.width-2, m.height-statusRows-1, tea.MouseWheelDown)
			if strings.Split(m.View().Content, "\n")[0] != header {
				t.Fatal("scroll moved the title")
			}
		}
		if len(seen) != 70 {
			t.Fatalf("reached %d members, want 70", len(seen))
		}
		if w.offset != 0 || m.windowOff != 0 || m.panelOff != 0 {
			t.Fatal("nicklist wheel moved another area")
		}
		x, y := visibleMember(t, m, "user0069")
		tapAt(m, x, y)
		if m.current().name != "user0069" {
			t.Fatal("scrolled row opened the wrong query")
		}
		m.selectWindow(1)
		m.View()
		for range 30 {
			wheelAt(m, m.width-2, 1, tea.MouseWheelUp)
		}
		x, y = visibleMember(t, m, "user0000")
		tapAt(m, x, y)
		if m.current().name != "user0000" {
			t.Fatal("cannot return to the first member")
		}
	}
}

// ```gherkin
// Scenario: Keep independent channel nicklist positions
//
//	Given two channels with long nicklists
//	And the first nicklist has been scrolled
//	When I switch channels and return
//	Then each nicklist retains its own position
//
// ```
func TestNicklistScrollIsIndependent(t *testing.T) {
	m := nickScrollModel(t, 70)
	first := m.current()
	wheelAt(m, m.width-2, 1, tea.MouseWheelDown)
	before := first.nickOff
	if before != 3 {
		t.Fatalf("one notch moved %d members, want 3", before)
	}
	deliver(t, m, ":tester!u@gloom.im JOIN #other")
	deliver(t, m, ":irc.gloom.im 353 tester = #other :alice bob")
	m.View()
	wheelAt(m, m.width-2, 1, tea.MouseWheelDown)
	visibleMember(t, m, "alice")
	m.selectWindow(1)
	m.View()
	if first != m.current() || before != first.nickOff {
		t.Fatal("channel switch lost the nicklist position")
	}
	// Chat wheel events and hidden panes must still scroll the chat.
	wheelAt(m, m.paneL+2, 2, tea.MouseWheelUp)
	if first.offset != 3 {
		t.Fatal("chat scrolling stopped working")
	}
	m.nicklist = false
	m.View()
	wheelAt(m, m.width-2, 2, tea.MouseWheelUp)
	if first.offset != 6 {
		t.Fatal("hidden nicklist intercepted chat scrolling")
	}
}

// ```gherkin
// Scenario: Clamp a scrolled nicklist after resizing or departures
//
//	Given a nicklist scrolled to its end
//	When more rows become available or members leave
//	Then the last available page is shown without blank overscroll
//
// ```
func TestNicklistScrollClampsAfterResizeAndMembershipChanges(t *testing.T) {
	m := nickScrollModel(t, 70)
	for range 30 {
		wheelAt(m, m.width-2, 1, tea.MouseWheelDown)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.View()
	visibleMember(t, m, "user0069")
	// The larger page must be full, including its first row.
	rows := strings.Split(m.View().Content, "\n")
	if !strings.Contains(ansi.Strip(rows[1]), "user0034") {
		t.Fatal("resize did not clamp to a full final page")
	}
	for i := 2; i < 70; i++ {
		deliver(t, m, fmt.Sprintf(":user%04d!u@gloom.im PART #gloom", i))
	}
	m.View()
	visibleMember(t, m, "user0000")
	visibleMember(t, m, "user0001")
	deliver(t, m, ":user0000!u@gloom.im NICK renamed")
	m.View()
	visibleMember(t, m, "renamed")
	deliver(t, m, ":user0001!u@gloom.im QUIT :bye")
	deliver(t, m, ":renamed!u@gloom.im QUIT :bye")
	m.View()
	wheelAt(m, m.width-2, 1, tea.MouseWheelDown)
	for _, h := range m.nickHits {
		if h.start > m.width-m.paneR {
			t.Fatal("empty nicklist retained a click target")
		}
	}
}

func BenchmarkNickRowsOverflow(b *testing.B) {
	for _, count := range []int{100, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			m := New("tester", false)
			w := m.current()
			for i := range count {
				n := fmt.Sprintf("user%05d", i)
				w.nicks[n] = n
			}
			m.nickRows(w, m.colors(), 30)
			b.ReportAllocs()
			for b.Loop() {
				m.nickRows(w, m.colors(), 30)
			}
		})
	}
}

func BenchmarkNicklistScroll(b *testing.B) {
	m := New("tester", false)
	m.gfx = gfxNone
	m.width, m.height = 120, 40
	m.selectWindow(m.ensure("#gloom"))
	w := m.current()
	for i := range 10000 {
		n := fmt.Sprintf("user%05d", i)
		w.nicks[n] = n
	}
	m.View()
	button := tea.MouseWheelDown
	b.ReportAllocs()
	for b.Loop() {
		if w.nickOff == 0 {
			button = tea.MouseWheelDown
		}
		if w.nickOff >= len(w.nicks)-(m.height-statusRows-1) {
			button = tea.MouseWheelUp
		}
		m.Update(tea.MouseWheelMsg{X: m.width - 2, Y: 1, Button: button})
		m.View()
	}
}
