package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// dragBetween presses, drags and releases, as a terminal reports it. The
// returned command is what the release asked Bubble Tea to do.
func dragBetween(m *Model, x0, y0, x1, y1 int) tea.Cmd {
	m.Update(tea.MouseClickMsg{X: x0, Y: y0, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x1, Y: y1, Button: tea.MouseLeft})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: x1, Y: y1, Button: tea.MouseLeft})
	return cmd
}

func copied(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command, so nothing was copied")
	}
	msg := cmd()
	if kind := fmt.Sprintf("%T", msg); kind != "tea.setClipboardMsg" {
		t.Fatalf("command produced %s, want a clipboard write", kind)
	}
	return fmt.Sprint(msg)
}

func screenRow(m *Model, y int) string {
	rows := strings.Split(m.View().Content, "\n")
	return strings.TrimRight(ansi.Strip(rows[y]), " ")
}

// colOf is the screen column where sub starts in row, counting cells and not
// bytes: the gutter rule is one column and three bytes.
func colOf(row, sub string) int {
	before, _, ok := strings.Cut(row, sub)
	if !ok {
		return -1
	}
	return ansi.StringWidth(before)
}

func rowWith(t *testing.T, m *Model, want string) int {
	t.Helper()
	for y := range m.height {
		if strings.Contains(screenRow(m, y), want) {
			return y
		}
	}
	t.Fatalf("no screen row contains %q", want)
	return 0
}

func chatModel(t *testing.T) *Model {
	t.Helper()
	m := New("tester", false)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.nicklist = false
	m.channels = false
	m.timestamps = false
	m.selectWindow(m.ensure("#gloom"))
	m.add(m.active, "alice", "first line of text")
	m.add(m.active, "bob", "second line of text")
	m.add(m.active, "carol", "third line of text")
	m.View()
	return m
}

func TestDragSelectsAndCopiesText(t *testing.T) {
	m := chatModel(t)
	last := rowWith(t, m, "third line of text")
	text := screenRow(m, last)
	from := colOf(text, "third")
	to := colOf(text, "text") + len("text") - 1
	got := copied(t, dragBetween(m, from, last, to, last))
	if got != "third line of text" {
		t.Fatalf("copied %q", got)
	}
}

func TestDragAcrossRowsCopiesEveryLine(t *testing.T) {
	m := chatModel(t)
	last := rowWith(t, m, "third line of text")
	a, b := screenRow(m, last-1), screenRow(m, last)
	// From the "s" of "second" on one row to the "d" of "third" on the next.
	got := copied(t, dragBetween(m, colOf(a, "second"), last-1, colOf(b, "third")+4, last))
	// The first row is cut at the start column and the last after the end
	// column. The nick and gutter before "third" are part of that last row, as
	// they are on screen.
	if got != a[strings.Index(a, "second"):]+"\n"+b[:strings.Index(b, "third")+5] {
		t.Fatalf("copied %q", got)
	}
}

func TestSelectionIsHighlightedAndCleared(t *testing.T) {
	m := chatModel(t)
	last := rowWith(t, m, "third line of text")
	text := screenRow(m, last)
	x := colOf(text, "third")
	plain := m.View().Content
	dragBetween(m, x, last, x+4, last)
	if m.View().Content == plain {
		t.Fatal("selection is not drawn")
	}
	for l := range strings.SplitSeq(m.View().Content, "\n") {
		if ansi.StringWidth(l) != 100 {
			t.Fatalf("highlight changed a line width to %d", ansi.StringWidth(l))
		}
	}
	if got := screenRow(m, last); got != text {
		t.Fatalf("highlight changed the text: %q", got)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.View().Content != plain {
		t.Fatal("Escape did not clear the selection")
	}
}

func TestSelectionWorksInServerAndDebugWindows(t *testing.T) {
	m := New("tester", false)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.nicklist, m.channels = false, false
	m.add(0, "", "server notice one")
	m.View()
	last := rowWith(t, m, "server notice one")
	text := screenRow(m, last)
	if got := copied(t, dragBetween(m, colOf(text, "server"), last, ansi.StringWidth(text)-1, last)); got != "server notice one" {
		t.Fatalf("server window copied %q", got)
	}

	m.toggleDebug()
	m.logRaw('<', "PING :irc.gloom.im")
	m.View()
	last = rowWith(t, m, "PING :irc.gloom.im")
	text = screenRow(m, last)
	got := copied(t, dragBetween(m, colOf(text, "PING"), last, ansi.StringWidth(text)-1, last))
	if got != "PING :irc.gloom.im" {
		t.Fatalf("debug window copied %q", got)
	}
}

func TestClickOnNickStillOpensQueryButDragDoesNot(t *testing.T) {
	m := chatModel(t)
	last := rowWith(t, m, "third line of text")
	text := screenRow(m, last)
	x := colOf(text, "carol")
	before := len(m.windows)
	dragBetween(m, x, last, x+20, last)
	if len(m.windows) != before || m.windows[m.active].name != "#gloom" {
		t.Fatal("a drag that began on a nick opened a query")
	}
	tapAt(m, x, last)
	if m.windows[m.active].name != "carol" {
		t.Fatalf("click on a nick selected %q", m.windows[m.active].name)
	}
}

func TestSelectionSurvivesScrollingAndClearsOnResize(t *testing.T) {
	m := chatModel(t)
	for range 40 {
		m.add(m.active, "dave", "filler line to scroll past")
	}
	m.View()
	last := m.height - bottomRows - inputPadRows - 2
	text := screenRow(m, last)
	copiedText := copied(t, dragBetween(m, 0, last-1, ansi.StringWidth(text), last))
	if strings.Count(copiedText, "\n") != 1 {
		t.Fatalf("expected two lines, got %q", copiedText)
	}
	m.scroll(5)
	if m.sel.win == nil {
		t.Fatal("scrolling cleared the selection")
	}
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 20})
	m.View()
	if m.sel.win != nil {
		t.Fatal("a resize rewraps the rows and must clear the selection")
	}
}
