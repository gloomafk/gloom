package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func navigationModel() *Model {
	m := New("tester", false)
	m.gfx = gfxNone
	m.width, m.height = 100, 20
	for i := range 30 {
		m.ensure(fmt.Sprintf("#channel%02d", i))
	}
	m.selectWindow(0)
	m.View()
	return m
}

func tabHit(m *Model, index int) (hit, bool) {
	_, hits := m.status()
	for _, h := range hits {
		if h.index == index && h.start < h.end {
			return h, true
		}
	}
	return hit{}, false
}

func wheelAt(m *Model, x, y int, button tea.MouseButton) {
	m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: button})
	m.View()
}

func TestEveryOverflowTabCanBeScrolledIntoViewAndClicked(t *testing.T) {
	for _, graphics := range []graphics{gfxNone, gfxKitty} {
		m := navigationModel()
		m.gfx = graphics
		if graphics == gfxKitty {
			m.drawLogo()
		}
		// A tab wider than the viewport must not trap the tabs after it.
		m.windows[1].name = "#" + strings.Repeat("界", 100)
		for i := 1; i < len(m.windows); i++ {
			for tries := 0; tries < len(m.windows); tries++ {
				if _, ok := tabHit(m, i); ok {
					break
				}
				wheelAt(m, 20, m.height-2, tea.MouseWheelDown)
			}
			h, ok := tabHit(m, i)
			if !ok {
				t.Fatalf("graphics %d: window %d has no tab", graphics, i)
			}
			tapAt(m, h.start, m.height-2)
			m.View()
			if m.active != i {
				t.Fatalf("clicked window %d, selected %d", i, m.active)
			}
			bar, hits := m.status()
			for _, row := range bar {
				if ansi.StringWidth(row) != m.width {
					t.Fatal("scrolling changed status bar width")
				}
			}
			for _, h := range hits {
				if h.index == hitLayout && h.start != m.width-iconCols {
					t.Fatal("scrolling moved the fixed buttons")
				}
				if h.index == hitLogo && h.start != 0 {
					t.Fatal("scrolling moved the logo")
				}
			}
		}
		for range 100 {
			wheelAt(m, 20, m.height-2, tea.MouseWheelUp)
		}
		if _, ok := tabHit(m, 1); !ok {
			t.Fatal("cannot scroll back to the first tab")
		}
		if m.current().offset != 0 {
			t.Fatal("tab scrolling moved chat scrollback")
		}
	}
}

func TestOverflowWindowsPaneScrollAndClick(t *testing.T) {
	for _, view := range []viewMode{viewWindow, viewList} {
		m := navigationModel()
		m.channels = true
		m.view = view
		m.list = []listEntry{{name: "#wrong"}, {name: "#also-wrong"}}
		m.View()
		for range 100 {
			wheelAt(m, 1, 5, tea.MouseWheelDown)
		}
		last := len(m.windows) - 1
		y := last - m.windowOff + 1
		rows := strings.Split(m.View().Content, "\n")
		if y >= m.height-statusRows || !strings.Contains(ansi.Strip(rows[y]), m.windows[last].name) {
			t.Fatal("last window is not visible after scrolling")
		}
		// Even with the channel list open, the pane click selects a window.
		tapAt(m, 1, 2)
		if m.active != m.windowOff+1 {
			t.Fatal("scrolled pane click selected the wrong window")
		}
		m.View()
		tapAt(m, 1, y)
		if m.active != last {
			t.Fatalf("last pane row selected window %d", m.active)
		}
		for range 100 {
			wheelAt(m, 1, 5, tea.MouseWheelUp)
		}
		if m.windowOff != 0 || m.current().offset != 0 || m.panelOff != 0 {
			t.Fatal("pane scroll did not clamp or moved the current view")
		}
		tapAt(m, 1, 1)
		if m.active != 0 {
			t.Fatal("cannot select the first window after scrolling back")
		}
	}
}

func TestSelectedWindowRevealedAfterResizeAndRemoval(t *testing.T) {
	m := navigationModel()
	last := len(m.windows) - 1
	m.selectWindow(last)
	m.View()
	if _, ok := tabHit(m, last); !ok {
		t.Fatal("selected tab was not revealed")
	}
	m.channels = true
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m.View()
	if last < m.windowOff || last >= m.windowOff+m.height-statusRows-1 {
		t.Fatal("selected window was not revealed after resize")
	}
	for len(m.windows) > 2 {
		m.closeWindow(len(m.windows) - 1)
	}
	m.View()
	if m.windowOff != 0 {
		t.Fatal("pane retained an invalid offset after removal")
	}
	m.channels = false
	m.View()
	if _, ok := tabHit(m, 1); !ok {
		t.Fatal("tab offset was not clamped after removal")
	}
}

func TestNavigationWheelClampsAndHandlesHorizontalScrolling(t *testing.T) {
	m := navigationModel()
	for range 100 {
		wheelAt(m, 20, m.height-2, tea.MouseWheelRight)
	}
	if _, ok := tabHit(m, len(m.windows)-1); !ok {
		t.Fatal("horizontal scrolling did not reach the final tab")
	}
	for range 100 {
		wheelAt(m, 20, m.height-2, tea.MouseWheelLeft)
	}
	if m.tabOff != 1 || m.current().offset != 0 {
		t.Fatal("horizontal scrolling did not clamp or changed scrollback")
	}
	for len(m.windows) > 3 {
		m.closeWindow(len(m.windows) - 1)
	}
	m.View()
	wheelAt(m, 20, m.height-2, tea.MouseWheelDown)
	if m.tabOff != 1 {
		t.Fatal("scrolling hid tabs even though every tab fits")
	}
}

func TestNavigationScrollSurvivesBackgroundWindowInsertion(t *testing.T) {
	m := New("tester", false)
	m.gfx = gfxNone
	m.width, m.height = 100, 20
	for i := range 30 {
		m.ensure(fmt.Sprintf("person%02d", i))
	}
	m.selectWindow(1)
	m.View()
	for range 10 {
		wheelAt(m, 20, m.height-2, tea.MouseWheelDown)
	}
	m.channels = true
	m.View()
	for range 3 {
		wheelAt(m, 1, 5, tea.MouseWheelDown)
	}
	firstTab, firstRow := m.windows[m.tabOff], m.windows[m.windowOff]
	deliver(t, m, ":other!u@gloom.im JOIN #new")
	m.View()
	if m.windows[m.tabOff] != firstTab || m.windows[m.windowOff] != firstRow {
		t.Fatal("background insertion moved the navigation away from its visible windows")
	}
	m.closeWindow(m.find("#new"))
	m.View()
	if m.windows[m.tabOff] != firstTab || m.windows[m.windowOff] != firstRow {
		t.Fatal("background removal moved the navigation away from its visible windows")
	}
}
