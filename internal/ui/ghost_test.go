package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestWelcomeGhostPaintsBlankCells(t *testing.T) {
	for _, p := range []palette{darkPalette, lightPalette} {
		g := welcomeGhost{padding: 3}
		for _, row := range g.rows(p, 140) {
			if bad := unpainted(row); bad != "" {
				t.Fatalf("ghost has cells without a background: %q", bad)
			}
		}
	}
}

func TestWelcomeGhostLifecycle(t *testing.T) {
	for _, paste := range []bool{false, true} {
		m := New("tester", false)
		m.width, m.height = 100, 40
		m.View()
		before := strings.Join(m.chatRows.rows, "\n")
		var cmd tea.Cmd
		for range 8 {
			_, cmd = m.update(ghostTick{})
		}
		if cmd == nil {
			t.Fatal("animation did not schedule another frame")
		}
		m.View()
		after := strings.Join(m.chatRows.rows, "\n")
		if before == after {
			t.Fatal("idle ghost did not animate")
		}
		var msg tea.Msg = tea.KeyPressMsg{Code: '/', Text: "/"}
		if paste {
			msg = tea.PasteMsg{Content: "/help"}
		}
		m.Update(msg)
		frame := m.ghost.frame
		_, cmd = m.update(ghostTick{})
		if cmd != nil || m.ghost.frame != frame {
			t.Fatal("typing did not stop animation")
		}
		m.note(0, "after ghost")
		rows, _ := m.messageRows(m.windows[0], m.colors(), 100)
		if !strings.Contains(ansi.Strip(rows[len(rows)-1]), "after ghost") {
			t.Fatal("message did not follow ghost")
		}
		m.selectWindow(m.openServer("irc.gloom.im"))
		rows, _ = m.messageRows(m.current(), m.colors(), 100)
		if len(rows) != 0 {
			t.Fatal("ghost leaked into server window")
		}
	}
}

func TestWelcomeGhostHighlightAndBounds(t *testing.T) {
	m := New("tester", false)
	g := welcomeGhost{}
	highlighted := false
	for frame := range 100 {
		g.frame = frame
		rows := g.rows(m.colors(), 40)
		if len(rows) != 18 {
			t.Fatal("incorrect silhouette height")
		}
		for _, row := range rows {
			if ansi.StringWidth(row) != 40 {
				t.Fatal("incorrect row width")
			}
			if strings.Contains(row, style(m.colors().accent, m.colors().bg).Bold(true).Render("g")) {
				highlighted = true
			}
		}
	}
	if !highlighted {
		t.Fatal("gloom was never highlighted")
	}
	m.height = 12
	rows, _ := m.messageRows(m.current(), m.colors(), 40)
	if len(rows) >= 18 {
		t.Fatal("ghost crowded a small terminal")
	}
}

func TestWelcomeGhostScrollbackEviction(t *testing.T) {
	m := New("tester", false)
	m.height = 40
	m.ghost.frozen = true
	for range maxLines {
		m.note(0, "later status")
	}
	rows, _ := m.messageRows(m.current(), m.colors(), 100)
	if len(rows) != maxLines {
		t.Fatal("ghost survived welcome eviction")
	}
}

func TestFrozenGhostSurvivesResize(t *testing.T) {
	m := New("tester", false)
	m.width, m.height = 100, 40
	m.View()
	m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	before, _ := m.messageRows(m.current(), m.colors(), 100)
	m.Update(tea.WindowSizeMsg{Width: 35, Height: 12})
	after, _ := m.messageRows(m.current(), m.colors(), 35)
	if len(after) < 18 || len(after) < len(before) {
		t.Fatal("resize removed frozen ghost history")
	}
	for _, row := range after {
		if ansi.StringWidth(row) != 35 {
			t.Fatal("frozen ghost overflowed narrow viewport")
		}
	}
}

func TestGhostWidenedSilhouette(t *testing.T) {
	g := welcomeGhost{}
	rows := g.rows(New("tester", false).colors(), 60)
	if got := len(strings.TrimSpace(ansi.Strip(rows[4]))); got != 38 {
		t.Fatalf("body width = %d, want 38 columns", got)
	}
	if got := len(strings.TrimSpace(ansi.Strip(rows[0]))); got != 21 {
		t.Fatalf("crown width = %d, want 21 columns", got)
	}
}

func TestGhostTailGap(t *testing.T) {
	g := welcomeGhost{}
	for frame := range 100 {
		g.frame = frame
		row := ansi.Strip(g.rows(New("tester", false).colors(), 42)[14])
		if strings.TrimSpace(row[30:]) != "" {
			t.Fatalf("frame %d has a stray character above the tail: %q", frame, row)
		}
	}
}

func TestGhostBalancedPaddingBeforeResponse(t *testing.T) {
	for _, height := range []int{30, 40, 51} {
		m := New("tester", false)
		m.width, m.height = 100, height
		m.View()
		m.Update(tea.PasteMsg{Content: "test"})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m.View()
		rows := m.chatRows.rows
		first, last, response := -1, -1, -1
		for i, row := range rows {
			text := strings.TrimSpace(ansi.Strip(row))
			if strings.Contains(text, "Welcome to") || text == "" {
				continue
			}
			if strings.Contains(text, "Unknown command: test.") {
				response = i
				continue
			}
			if first < 0 {
				first = i
			}
			last = i
		}
		if first < 0 || response < 0 {
			t.Fatal("missing ghost or response")
		}
		above, below := first-1, response-last-1
		if above < 1 || above != below {
			t.Fatalf("height %d: padding above=%d below=%d", height, above, below)
		}
		if m.body.start != 0 {
			t.Fatal("first response pushed welcome off screen")
		}
	}
}
