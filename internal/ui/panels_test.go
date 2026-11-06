package ui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func sized(w, h int) *Model {
	m := New("tester", false)
	m.gfx = gfxNone
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.selectWindow(m.ensure("#gloom"))
	m.windows[m.active].joined = true
	m.windows[m.active].nicks["tester"] = "tester"
	m.add(m.active, "alice", "hello tester")
	return m
}

func press(t *testing.T, m *Model, index int, button tea.MouseButton) {
	t.Helper()
	_, hits := m.status()
	for _, h := range hits {
		if h.index == index {
			clickAt(m, h.start+1, m.height-2, button)
			return
		}
	}
	t.Fatalf("no status bar target %d", index)
}
func TestStatusButtons(t *testing.T) {
	m := sized(100, 24)
	press(t, m, hitChannels, tea.MouseLeft)
	if m.view != viewList {
		t.Fatal("channel button did not open the list")
	}
	press(t, m, hitChannels, tea.MouseLeft)
	if m.view != viewWindow {
		t.Fatal("channel button did not close the list")
	}
	press(t, m, hitConfig, tea.MouseLeft)
	if m.view != viewConfig || !strings.Contains(ansi.Strip(m.View().Content), "[server]") {
		t.Fatal("config view not shown")
	}
	press(t, m, hitConfig, tea.MouseLeft)
	press(t, m, hitDebug, tea.MouseLeft)
	if i := m.rawWindow(); i != len(m.windows)-1 || m.active != i {
		t.Fatal("debug window not open and last")
	}
	m.ensure("#later")
	if m.windows[len(m.windows)-1].name != "debug" {
		t.Fatal("debug window not kept last")
	}
	press(t, m, hitDebug, tea.MouseLeft)
	if m.rawWindow() >= 0 || m.windows[m.active].name != "#gloom" {
		t.Fatal("debug window not closed back to the previous window")
	}
}
func TestLayoutCyclesAndMenu(t *testing.T) {
	m := sized(100, 24)
	var order []string
	for range len(layouts) {
		press(t, m, hitLayout, tea.MouseLeft)
		order = append(order, m.layoutName())
		if m.layoutIndex() < 0 {
			t.Fatalf("cycled to a layout outside the table: %q", m.layoutName())
		}
	}
	want := []string{"nicks + channels", "channels", "status", "bare", "nicks + status"}
	if !slices.Equal(order, want) {
		t.Fatalf("layout cycle is %q, want %q", order, want)
	}
	press(t, m, hitLayout, tea.MouseRight)
	if !m.layMenu || !strings.Contains(ansi.Strip(m.View().Content), "Save as default") {
		t.Fatal("layout menu not shown")
	}
	clickAt(m, 5, 5, tea.MouseLeft)
	if m.layMenu {
		t.Fatal("menu not closed by a click")
	}
}

func TestFunctionKeysAreNotBound(t *testing.T) {
	for _, code := range []rune{tea.KeyF6, tea.KeyF7, tea.KeyF8, tea.KeyF9, tea.KeyF10, tea.KeyF12} {
		m := sized(100, 24)
		m.selectWindow(m.ensure("#gloom"))
		layout, nicks, stamps, channels := m.layoutName(), m.nicklist, m.timestamps, m.channels
		m.Update(tea.KeyPressMsg{Code: code})
		if m.view != viewWindow || m.layMenu || m.layoutName() != layout || m.nicklist != nicks ||
			m.timestamps != stamps || m.channels != channels {
			t.Errorf("%s changed the interface", tea.KeyPressMsg{Code: code}.String())
		}
	}
}
func TestLogoSelectsGloomWindowAndSplashIsACommand(t *testing.T) {
	m := sized(100, 24)
	m.selectWindow(m.ensure("#gloom"))
	press(t, m, hitLogo, tea.MouseLeft)
	if m.active != 0 || m.view != viewWindow {
		t.Fatal("logo did not select the gloom window")
	}
	m.command("/splash")
	out := ansi.Strip(m.View().Content)
	if m.view != viewSplash || !strings.Contains(out, "gloom v0.0.1") || !strings.Contains(out, "/splash") {
		t.Fatal("splash not shown")
	}
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.view != viewWindow || m.input.Value() != "" {
		t.Fatal("key did not leave the splash")
	}
	m.command("/splash")
	press(t, m, hitLogo, tea.MouseLeft)
	if m.view != viewWindow {
		t.Fatal("logo did not leave the splash")
	}
}
func TestChannelListFilterAndJoin(t *testing.T) {
	m := sized(100, 24)
	m.listing = true
	deliver(t, m, ":irc.gloom.im 321 tester Channel :Users Name")
	deliver(t, m, ":irc.gloom.im 322 tester #small 3 :tiny")
	deliver(t, m, ":irc.gloom.im 322 tester #rust 1284 :The Rust language")
	deliver(t, m, ":irc.gloom.im 322 tester #go 641 :Go programming")
	deliver(t, m, ":irc.gloom.im 323 tester :End of /LIST")
	if m.listing || len(m.list) != 3 || m.filteredList()[0].name != "#rust" {
		t.Fatal("list not collected and sorted by users")
	}
	m.toggleView(viewList)
	m.input.SetValue("prog")
	if hits := m.filteredList(); len(hits) != 1 || hits[0].name != "#go" {
		t.Fatal("filter by topic failed")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "1 of 3 channels") {
		t.Fatal("filtered count missing")
	}
	// Replies to a manual /list stay in the server window.
	m.list = nil
	deliver(t, m, ":irc.gloom.im 322 tester #x 1 :manual")
	if len(m.list) != 0 {
		t.Fatal("unrequested list reply captured")
	}
}
func TestViewsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {70, 14}, {40, 10}, {30, 8}, {20, 5}} {
		for name, setup := range map[string]func(m *Model){
			"chat":     func(m *Model) {},
			"list":     func(m *Model) { m.list = []listEntry{{"#a", 1, strings.Repeat("topic ", 40)}}; m.toggleView(viewList) },
			"config":   func(m *Model) { m.toggleView(viewConfig) },
			"splash":   func(m *Model) { m.view = viewSplash },
			"debug":    func(m *Model) { m.logRaw('>', strings.Repeat("PRIVMSG ", 30)); m.toggleDebug() },
			"chanlist": func(m *Model) { m.channels, m.sb = true, false },
			"menu":     func(m *Model) { m.layMenu = true },
		} {
			m := sized(size[0], size[1])
			setup(m)
			lines := strings.Split(m.View().Content, "\n")
			if len(lines) != size[1] {
				t.Fatalf("%s %v: %d lines", name, size, len(lines))
			}
			for _, l := range lines {
				if ansi.StringWidth(l) != size[0] {
					t.Fatalf("%s %v: width %d in %q", name, size, ansi.StringWidth(l), ansi.Strip(l))
				}
			}
		}
	}
}

func dragMouse(m *Model, from, to, y int) {
	clickAt(m, from, y, tea.MouseLeft)
	m.Update(tea.MouseMotionMsg{X: to, Y: y, Button: tea.MouseLeft})
}
func TestResizePanes(t *testing.T) {
	// Wide enough for both default panes and the 45 column message minimum.
	m := sized(200, 24)
	m.channels = true
	m.View()
	// Nicklist: its first column is the grip, and the pane follows the pointer.
	grip := m.width - m.nlCols
	dragMouse(m, grip, grip-6, 5)
	if m.nlCols != nicklistW+6 || m.drag != paneNicklist {
		t.Fatalf("nicklist width %d", m.nlCols)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, fmt.Sprintf("%d cols", nicklistW+6)) {
		t.Fatal("drag badge missing")
	}
	m.Update(tea.MouseMotionMsg{X: 0, Y: 5, Button: tea.MouseLeft})
	if m.nlCols != maxPane {
		t.Fatalf("not clamped to %d: %d", maxPane, m.nlCols)
	}
	m.Update(tea.MouseMotionMsg{X: m.width - 1, Y: 5, Button: tea.MouseLeft})
	if m.nlCols != minPane {
		t.Fatalf("not clamped to %d: %d", minPane, m.nlCols)
	}
	m.Update(tea.MouseReleaseMsg{X: 90, Y: 5, Button: tea.MouseLeft})
	if m.drag != paneNone {
		t.Fatal("release did not end the drag")
	}
	// Windows pane: the last column is the grip.
	m.View()
	dragMouse(m, m.clCols-1, 25, 5)
	if m.clCols != 26 {
		t.Fatalf("windows pane width %d", m.clCols)
	}
	m.Update(tea.MouseReleaseMsg{X: 25, Y: 5, Button: tea.MouseLeft})
	// A second press within the delay resets that pane.
	m.View()
	clickAt(m, m.clCols-1, 5, tea.MouseLeft)
	m.Update(tea.MouseReleaseMsg{X: m.clCols - 1, Y: 5, Button: tea.MouseLeft})
	clickAt(m, m.clCols-1, 5, tea.MouseLeft)
	if m.clCols != chanlistW || m.drag != paneNone {
		t.Fatalf("double press did not reset: %d", m.clCols)
	}
	for _, size := range [][2]int{{100, 24}, {70, 14}, {50, 10}} {
		m.nlCols, m.clCols = maxPane, maxPane
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != size[1] {
			t.Fatalf("%v: %d lines", size, len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) != size[0] {
				t.Fatalf("%v: width %d", size, ansi.StringWidth(l))
			}
		}
	}
}

var sgr = regexp.MustCompile("\x1b\\[([0-9;:]*)m")

// unpainted returns the first visible text drawn without an explicit background,
// which would show the terminal's own colour instead of the palette.
func unpainted(s string) string {
	bg, at := false, 0
	for _, loc := range sgr.FindAllStringSubmatchIndex(s, -1) {
		if text := strings.TrimSpace(strings.ReplaceAll(s[at:loc[0]], "\n", "")); text != "" && !bg {
			return text
		}
		if strings.Contains(s[loc[0]:loc[1]], "\n") {
			bg = false
		}
		params := s[loc[2]:loc[3]]
		switch {
		case params == "" || params == "0":
			bg = false
		case strings.Contains(params, "48;2;"):
			bg = true
		}
		at = loc[1]
	}
	if text := strings.TrimSpace(s[at:]); text != "" && !bg {
		return text
	}
	return ""
}
func TestEveryCellHasBackground(t *testing.T) {
	for _, size := range [][2]int{{100, 28}, {70, 14}} {
		for name, setup := range map[string]func(m *Model){
			"chat":     func(m *Model) {},
			"list":     func(m *Model) { m.list = []listEntry{{"#a", 1234, "topic"}}; m.toggleView(viewList) },
			"config":   func(m *Model) { m.toggleView(viewConfig) },
			"splash":   func(m *Model) { m.view = viewSplash },
			"debug":    func(m *Model) { m.logRaw('>', "NICK x"); m.toggleDebug() },
			"chanlist": func(m *Model) { m.channels, m.sb = true, false },
			"drag":     func(m *Model) { m.channels = true; m.drag = paneChanlist },
		} {
			m := sized(size[0], size[1])
			setup(m)
			for y, line := range strings.Split(m.View().Content, "\n") {
				if y == m.height-bottomRows-1 {
					continue // the input row holds the text cursor, drawn in reverse
				}
				if bad := unpainted(line); bad != "" {
					t.Fatalf("%s %v: text without background: %q in %q", name, size, bad, ansi.Strip(line))
				}
			}
		}
	}
}

func TestAltNumberKeysAreNotBound(t *testing.T) {
	for n := '1'; n <= '9'; n++ {
		m := sized(100, 24)
		m.ensure("#other")
		active := m.active
		key := tea.KeyPressMsg{Code: n, Mod: tea.ModAlt}
		if key.String() != "alt+"+string(n) {
			t.Fatalf("test sends %q, want alt+%c", key.String(), n)
		}
		m.Update(key)
		if m.active != active {
			t.Errorf("%s switched from window %d to %d", key.String(), active, m.active)
		}
	}
	m := sized(100, 24)
	if text := m.configText(); strings.Contains(text, "[keys]") || strings.Contains(strings.ToLower(text), "alt") {
		t.Errorf("config view still lists hotkeys:\n%s", text)
	}
}
