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

// ```gherkin
// Scenario: Scroll steadily with a trackpad
//
//	Given a joined channel with more members than fit in the nicklist
//	When the user swipes down over the nicklist with a trackpad
//	And the swipe also produces sideways scroll events
//	Then the nicklist moves down only, three rows per vertical event
//	And it never jumps back towards earlier members
//
// ```
func TestTrackpadSwipeScrollsNicklistSteadily(t *testing.T) {
	m := nickScrollModel(t, 70)
	m.channels = true
	m.View()
	w := m.current()
	// A slightly diagonal swipe interleaves sideways events in both directions.
	swipe := []tea.MouseButton{
		tea.MouseWheelDown, tea.MouseWheelLeft,
		tea.MouseWheelDown, tea.MouseWheelRight,
		tea.MouseWheelLeft, tea.MouseWheelDown,
	}
	want := 0
	for _, b := range swipe {
		prev := w.nickOff
		wheelAt(m, m.width-2, 2, b)
		if b == tea.MouseWheelDown {
			want += wheelRows
		}
		if w.nickOff < prev {
			t.Fatalf("%v moved the nicklist back from %d to %d", b, prev, w.nickOff)
		}
		if w.nickOff != want {
			t.Fatalf("after %v the nicklist is at %d, want %d", b, w.nickOff, want)
		}
	}
	if w.offset != 0 || m.windowOff != 0 || m.tabOff > 1 {
		t.Fatal("the nicklist swipe moved another area")
	}
	// Sideways events over the windows pane and the chat move nothing either.
	m.ensure("#second")
	m.ensure("#third")
	for _, b := range []tea.MouseButton{tea.MouseWheelRight, tea.MouseWheelLeft, tea.MouseWheelRight} {
		wheelAt(m, 1, 2, b)
		wheelAt(m, m.paneL+2, 2, b)
	}
	if m.windowOff != 0 || w.offset != 0 || w.nickOff != want {
		t.Fatal("sideways events scrolled a vertical list")
	}
}

// ```gherkin
// Scenario: Keep member rows current while scrolling
//
//	Given a nicklist that has been scrolled through
//	When a NAMES reply changes a prefix, the user changes nick or the theme changes
//	Then the visible rows show the new prefix, highlight and colours
//
// ```
func TestNicklistRowsFollowChangesAfterScrolling(t *testing.T) {
	m := nickScrollModel(t, 70)
	nickColumn := func() []string {
		rows := strings.Split(m.View().Content, "\n")
		out := make([]string, 0, len(rows))
		for _, r := range rows[:m.height-statusRows] {
			out = append(out, ansi.Cut(r, m.width-m.paneR, m.width))
		}
		return out
	}
	row := func(name string) string {
		for _, r := range nickColumn() {
			if strings.Contains(ansi.Strip(r), name) {
				return r
			}
		}
		t.Fatalf("member %q is not visible", name)
		return ""
	}
	// Scroll down and back so every early row is cached.
	for range 5 {
		wheelAt(m, m.width-2, 2, tea.MouseWheelDown)
	}
	for range 5 {
		wheelAt(m, m.width-2, 2, tea.MouseWheelUp)
	}
	names := make([]string, 70)
	for i := range names {
		names[i] = fmt.Sprintf("user%04d", i)
	}
	names[3] = "+" + names[3]
	deliver(t, m, ":irc.gloom.im 353 tester = #gloom :tester "+strings.Join(names, " "))
	deliver(t, m, ":irc.gloom.im 366 tester #gloom :End of /NAMES list.")
	if !strings.Contains(ansi.Strip(nickColumn()[1]), "+ user0003") {
		t.Fatal("a new voice prefix did not reach the top of the cached list")
	}

	cols := m.nlCols - 1
	if !strings.Contains(row("tester"), nickRow("tester", m.colors(), cols, "tester")) {
		t.Fatal("the own nick is not highlighted")
	}
	deliver(t, m, ":tester!u@gloom.im NICK aaself")
	if !strings.Contains(row("aaself"), nickRow("aaself", m.colors(), cols, "aaself")) {
		t.Fatal("the new own nick is not highlighted")
	}
	if nickRow("aaself", m.colors(), cols, "aaself") == nickRow("aaself", m.colors(), cols, "tester") {
		t.Fatal("the highlight is not visible in a row")
	}

	dark := row("user0002")
	m.light = true
	if row("user0002") == dark {
		t.Fatal("a theme change kept the cached dark row")
	}
}

// BenchmarkNicklistScrollRealistic scrolls a busy channel's nicklist beside
// chat history at a common window size, as a trackpad swipe does.
func BenchmarkNicklistScrollRealistic(b *testing.B) {
	m := New("tester", false)
	m.gfx = gfxNone
	m.width, m.height = 200, 50
	m.channels = true
	m.selectWindow(m.ensure("#gloom"))
	w := m.current()
	for i := range 454 {
		n := fmt.Sprintf("user%04d", i)
		w.nicks[n] = n
	}
	for range 200 {
		m.add(m.active, "user0000", "chat history line")
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
