package ui

import (
	"strings"
	"testing"
	"testing/synctest"

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

func TestHiddenWelcomeStopsTicks(t *testing.T) {
	for name, hide := range map[string]func(*Model){
		"other window": func(m *Model) { m.selectWindow(m.openServer("irc.gloom.im")) },
		"list":         func(m *Model) { m.toggleView(viewList) },
		"config":       func(m *Model) { m.toggleView(viewConfig) },
		"splash":       func(m *Model) { m.toggleView(viewSplash) },
		"scrollback":   scrollBack,
		"selection":    func(m *Model) { m.sel.down = true },
		"startup":      func(m *Model) { m.starting = true },
		"too narrow":   func(m *Model) { m.width = ghostWidth - 1 },
		"too short":    func(m *Model) { m.height = 20 },
		"sidebar":      func(m *Model) { m.width, m.channels = 70, true },
		"cleared":      func(m *Model) { m.command("/clear") },
		"evicted": func(m *Model) {
			for range maxLines {
				m.note(0, "later status")
			}
		},
		"below viewport": func(m *Model) {
			for range 40 {
				m.note(0, "later status")
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := New("tester", false)
			m.gfx = gfxNone
			m.width, m.height = 100, 40
			m.View()
			hide(m)
			frame, version := m.ghost.frame, m.windows[0].rowsVersion
			_, cmd := m.Update(ghostTick{})
			if cmd != nil {
				t.Fatal("hidden welcome scheduled another command")
			}
			if m.ghost.frame != frame || m.windows[0].rowsVersion != version {
				t.Fatal("hidden welcome advanced or invalidated history")
			}
		})
	}
}

func TestWelcomeTickPauseAndResume(t *testing.T) {
	for name, hide := range map[string]func(*Model){
		"window":    func(m *Model) { m.selectWindow(m.openServer("irc.gloom.im")) },
		"panel":     func(m *Model) { m.toggleView(viewConfig) },
		"size":      func(m *Model) { m.Update(tea.WindowSizeMsg{Width: 40, Height: 20}) },
		"selection": func(m *Model) { m.sel.down = true },
		"history":   scrollBack,
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := New("tester", false)
				m.gfx = gfxNone
				_, pending := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
				if pending == nil {
					t.Fatal("visible welcome did not schedule its first tick")
				}
				hide(m)
				_, cmd := m.Update(pending())
				if cmd != nil || m.ghost.pending || m.ghost.frame != 0 {
					t.Fatal("pending tick continued the hidden animation")
				}
				_, cmd = m.Update(nil)
				if cmd != nil {
					t.Fatal("unrelated update restarted the hidden animation")
				}
				m.selectWindow(0)
				m.sel.down = false
				m.windows[0].offset = 0
				_, resumed := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
				if resumed == nil || !m.ghost.pending {
					t.Fatal("returning to the welcome did not restart its timer")
				}
				_, cmd = m.Update(nil)
				if cmd != nil {
					t.Fatal("unrelated update scheduled a duplicate tick")
				}
				for want := 1; want <= 3; want++ {
					_, resumed = m.Update(resumed())
					if resumed == nil || m.ghost.frame != want {
						t.Fatalf("resumed frame = %d, want %d with a next tick", m.ghost.frame, want)
					}
				}
			})
		})
	}
}

// scrollBack overflows the status window by a few rows and scrolls back one,
// so View keeps the offset rather than clamping it to zero.
func scrollBack(m *Model) {
	for range 3 {
		m.note(0, "later status")
	}
	m.windows[0].offset = 1
}

func TestScrollBackKeepsOffset(t *testing.T) {
	m := New("tester", false)
	m.gfx = gfxNone
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	scrollBack(m)
	m.View()
	if m.windows[0].offset != 1 {
		t.Fatalf("offset = %d, want View to keep 1", m.windows[0].offset)
	}
}

func TestWelcomeResumesWhenViewClampsScrollback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New("tester", false)
		m.gfx = gfxNone
		m.note(0, "a later status line that wraps in a narrow terminal but fits on one row in a wide one")
		_, pending := m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
		if pending == nil {
			t.Fatal("missing initial tick")
		}
		m.windows[0].offset = 1
		m.View()
		if m.windows[0].offset != 1 {
			t.Fatal("narrow status window did not overflow")
		}
		_, cmd := m.Update(pending())
		if cmd != nil || m.ghost.pending {
			t.Fatal("scrolled back welcome continued animating")
		}
		_, resumed := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		if resumed == nil || !m.ghost.pending {
			t.Fatal("widening until every row fits did not resume animation")
		}
		m.View()
		if m.windows[0].offset != 0 {
			t.Fatal("View kept scrollback with every row visible")
		}
		_, resumed = m.Update(resumed())
		if resumed == nil || m.ghost.frame != 1 {
			t.Fatal("resumed welcome did not advance")
		}
	})
}

func TestWelcomeQuickReturnKeepsOneTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New("tester", false)
		m.gfx = gfxNone
		_, pending := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		if pending == nil {
			t.Fatal("missing initial tick")
		}
		for range 5 {
			m.toggleView(viewConfig)
			_, cmd := m.Update(nil)
			if cmd != nil {
				t.Fatal("hiding welcome scheduled another tick")
			}
			_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if cmd != nil || !m.showing(0) {
				t.Fatal("returning before the pending tick created another timer")
			}
		}
		_, cmd := m.Update(pending())
		if cmd == nil || m.ghost.frame != 1 {
			t.Fatal("the original pending tick did not continue animation")
		}
	})
}

func TestFrozenWelcomeDoesNotRestart(t *testing.T) {
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: '/', Text: "/"},
		tea.PasteMsg{Content: "/help"},
	} {
		synctest.Test(t, func(t *testing.T) {
			m := New("tester", false)
			m.gfx = gfxNone
			_, pending := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
			if pending == nil {
				t.Fatal("missing initial tick")
			}
			m.Update(msg)
			_, cmd := m.Update(pending())
			if cmd != nil || m.ghost.frame != 0 || m.ghost.pending {
				t.Fatal("typed welcome continued animating")
			}
			m.toggleView(viewConfig)
			m.Update(nil)
			_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if cmd != nil || !m.showing(0) {
				t.Fatal("returning to a frozen welcome restarted animation")
			}
		})
	}
}

func TestWelcomeWaitsForStartup(t *testing.T) {
	m := New("tester", false)
	m.gfx = gfxKitty
	m.Init()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if m.ghost.pending {
		t.Fatal("animation started before startup graphics were queued")
	}
	m.Update(tea.RawMsg{Msg: imageOutput("ready")})
	if m.starting || !m.ghost.pending {
		t.Fatal("animation did not start when the startup gate opened")
	}
}
