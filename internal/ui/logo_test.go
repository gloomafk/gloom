package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func env(kv ...string) func(string) string {
	return func(k string) string {
		for i := 0; i+1 < len(kv); i += 2 {
			if kv[i] == k {
				return kv[i+1]
			}
		}
		return ""
	}
}
func TestDetectGraphics(t *testing.T) {
	for _, c := range []struct {
		name string
		env  func(string) string
		want graphics
	}{
		{"ghostty", env("TERM_PROGRAM", "ghostty"), gfxKitty},
		{"kitty", env("TERM", "xterm-kitty"), gfxKitty},
		{"rex", env("TERM_PROGRAM", "rex", "TERM", "xterm-256color"), gfxKitty},
		{"rex term fallback", env("TERM", "xterm-rex"), gfxKitty},
		{"tmux inside rex", env("TERM_PROGRAM", "rex", "TMUX", "/tmp/x"), gfxNone},
		{"screen inside rex", env("TERM_PROGRAM", "rex", "STY", "x"), gfxNone},
		{"rex override off", env("TERM_PROGRAM", "rex", "GLOOM_GRAPHICS", "off"), gfxNone},
		{"rex override iterm2", env("TERM_PROGRAM", "rex", "GLOOM_GRAPHICS", "iterm2"), gfxITerm2},
		{"unrelated terminal", env("TERM_PROGRAM", "rex-other", "TERM", "xterm-rex-other"), gfxNone},
		{"iterm", env("TERM_PROGRAM", "iTerm.app"), gfxITerm2},
		{"tmux inside ghostty", env("TERM_PROGRAM", "ghostty", "TMUX", "/tmp/x"), gfxNone},
		{"plain xterm", env("TERM", "xterm-256color"), gfxNone},
		{"override off", env("TERM_PROGRAM", "ghostty", "GLOOM_GRAPHICS", "off"), gfxNone},
		{"override kitty", env("GLOOM_GRAPHICS", "kitty", "TMUX", "x"), gfxKitty},
		{"Terminal.app ignores kitty override", env("TERM_PROGRAM", "Apple_Terminal", "GLOOM_GRAPHICS", "kitty"), gfxNone},
		{"Terminal.app ignores iterm2 override", env("TERM_PROGRAM", "Apple_Terminal", "GLOOM_GRAPHICS", "iterm2"), gfxNone},
	} {
		if got := detectGraphics(c.env); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}
func TestKittySequences(t *testing.T) {
	m := New("tester", false)
	m.gfx = gfxKitty
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	first := m.logoSequence()
	for _, want := range []string{"a=t,f=100,i=1", fmt.Sprintf("a=p,U=1,i=1,p=1,c=%d,r=%d", logoCols, statusRows), fmt.Sprintf("c=%d,r=%d", iconCols, statusRows)} {
		if !strings.Contains(first, want) {
			t.Fatalf("missing %q in %q", want, first[:min(len(first), 300)])
		}
	}
	if strings.Contains(first, "\x1b[") && strings.Contains(first, "a=d") {
		t.Fatal("images deleted while being set up")
	}
	// Every look of every icon is sent up front, so nothing is sent later.
	if n := strings.Count(first, "a=t"); n != 1+3*2+8 {
		t.Fatalf("%d images sent, want %d", n, 1+3*2+8)
	}
	again := m.logoSequence()
	if strings.Contains(again, "a=t") || !strings.Contains(again, "a=p,U=1") {
		t.Fatal("a resize must only place again, never resend images")
	}
	m.light = true
	if seq := m.logoSequence(); !strings.Contains(seq, "a=t") {
		t.Fatal("light theme images not sent")
	}
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	if seq := m.logoSequence(); seq != "" {
		t.Fatalf("output on a terminal too small for images: %q", seq)
	}
}
func TestITerm2Sequence(t *testing.T) {
	m := New("tester", false)
	m.gfx = gfxITerm2
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if seq := m.logoSequence(); !strings.Contains(seq, "\x1b]1337;File=") || !strings.Contains(seq, "inline=1") || !strings.Contains(seq, fmt.Sprintf("\x1b[%d;1H", 30-statusRows+1)) {
		t.Fatal("iTerm2 sequence missing")
	}
}
func TestIconsRender(t *testing.T) {
	for _, name := range []string{"channels", "config", "debug", "layout"} {
		if png := renderIcon(name, "#5E9BFF", layoutChannels|layoutNicks, "#5E9BFF"); len(png) < 200 {
			t.Fatalf("%s icon is empty", name)
		}
	}
}
func TestLogoTextFallback(t *testing.T) {
	m := New("tester", false)
	m.gfx = gfxNone
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	bar := statusBar(m)
	if got := ansi.Strip(bar[statusTextRow]); !strings.HasPrefix(got, " GLOOM ") {
		t.Fatalf("title without images is %q, want it to start with \" GLOOM \"", got)
	}
	for r, row := range bar {
		for _, c := range ansi.Strip(row) {
			if c >= 0x2800 && c <= 0x28FF {
				t.Fatalf("braille art on bar row %d", r)
			}
		}
	}
	// With images wanted, the mark's slot is blank before the images are sent.
	m.gfx = gfxKitty
	if got := ansi.Strip(statusBar(m)[statusTextRow]); !strings.HasPrefix(got, strings.Repeat(" ", logoCols)+"GLOOM ") {
		t.Fatalf("Kitty bar before the images are sent starts %q", got)
	}
}

// The first draw must not wait for a timer or change the bar's shape, or the
// logo blips from braille art to the image.
func TestKittyFirstDrawIsImmediateAndKeepsTheBarShape(t *testing.T) {
	m := New("tester", false)
	m.gfx = gfxKitty
	render := m.renderSprites() // what Init does
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.logoPending || m.logoSent {
		t.Fatal("a draw was scheduled or sent before the images were encoded")
	}
	_, beforeHits := m.status()
	before := ansi.Strip(strings.Join(statusBar(m), "\n"))
	ready, ok := render().(spritesReady)
	if !ok || len(ready) != 15 {
		t.Fatalf("expected 15 encoded images, got %d", len(ready))
	}
	if m.logoSent {
		t.Fatal("images drawn before they were encoded")
	}
	_, cmd := m.Update(ready)
	if cmd == nil || !m.logoSent || m.logoPending {
		t.Fatal("images were not drawn as soon as they were encoded")
	}
	_, afterHits := m.status()
	if !slices.Equal(beforeHits, afterHits) {
		t.Fatalf("bar changed shape when the images arrived:\n%v\n%v", beforeHits, afterHits)
	}
	for i, row := range strings.Split(ansi.Strip(strings.Join(statusBar(m), "\n")), "\n") {
		if w := ansi.StringWidth(row); w != ansi.StringWidth(strings.Split(before, "\n")[i]) {
			t.Fatalf("row %d changed width from %d to %d", i, ansi.StringWidth(strings.Split(before, "\n")[i]), w)
		}
	}
}

func TestStartupWaitsForSizeAndImageOutput(t *testing.T) {
	for _, imagesFirst := range []bool{false, true} {
		m := New("tester", false)
		m.gfx = gfxKitty
		m.Init()
		assertWaiting := func() {
			t.Helper()
			v := m.View()
			if v.Content != "" || !v.AltScreen {
				t.Fatal("startup exposed an incomplete frame")
			}
		}
		assertWaiting()
		ready := m.renderSprites()()
		if imagesFirst {
			m.Update(ready)
			assertWaiting()
		}
		_, upload := m.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
		assertWaiting()
		if !imagesFirst {
			_, upload = m.Update(ready)
			assertWaiting()
		}
		m.Update(arrowPointer()())
		assertWaiting()
		// Deliver the actual upload command as the event loop does.
		var deliverOutput func(tea.Cmd)
		deliverOutput = func(cmd tea.Cmd) {
			if cmd == nil {
				return
			}
			msg := cmd()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, child := range batch {
					deliverOutput(child)
				}
				return
			}
			m.Update(msg)
		}
		deliverOutput(upload)
		v := m.View()
		lines := strings.Split(v.Content, "\n")
		if len(lines) != 45 || !strings.Contains(v.Content, placeholder) {
			t.Fatal("first frame is missing its actual height or images")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != 140 {
				t.Fatal("first frame used provisional width")
			}
		}
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		if m.View().Content == "" {
			t.Fatal("resize blanked an already visible screen")
		}
	}
}

func TestStartupFallbackDoesNotWaitForImages(t *testing.T) {
	for _, gfx := range []graphics{gfxNone, gfxKitty, gfxITerm2} {
		m := New("tester", false)
		m.gfx = gfx
		m.Init()
		width, height := 140, 45
		if gfx == gfxKitty {
			width, height = 20, 5
		}
		m.Update(tea.WindowSizeMsg{Width: width, Height: height})
		if m.View().Content == "" {
			t.Fatal("fallback waited for images")
		}
	}
}

// width of one placeholder cell must be a single column, or the bar would drift.
func TestPlaceholderIsOneCell(t *testing.T) {
	for _, c := range []int{0, 5} {
		if w := ansi.StringWidth(placeholderRow(7, 2, 6, "#5E9BFF")); w != 6 {
			t.Fatalf("row of 6 cells is %d wide (col %d)", w, c)
		}
	}
}
func TestKittyStatusBarUsesPlaceholders(t *testing.T) {
	m := New("tester", false)
	m.gfx = gfxKitty
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.drawLogo()
	out := m.View().Content
	if got := strings.Count(out, placeholder); got != logoCols*statusRows+4*iconCols*statusRows {
		t.Fatalf("%d placeholder cells", got)
	}
	for l := range strings.SplitSeq(out, "\n") {
		if ansi.StringWidth(l) != 100 {
			t.Fatalf("line is %d wide", ansi.StringWidth(l))
		}
	}
	// Nothing about the images changes with a resize, so nothing can blink.
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	if !strings.Contains(m.View().Content, placeholder) {
		t.Fatal("images dropped while resizing")
	}
	// Toggling a view must switch icons without sending anything.
	m.toggleView(viewConfig)
	if !strings.Contains(m.View().Content, placeholder) || strings.Contains(m.logoSequence(), "a=t") {
		t.Fatal("icon change needed a new image")
	}
}

func TestResizeSchedulesCheapReplace(t *testing.T) {
	m := New("tester", false)
	m.gfx = gfxKitty
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.drawLogo()
	if cmd := m.scheduleLogo(); cmd != nil {
		t.Fatal("redraw scheduled without a change")
	}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	if cmd == nil {
		t.Fatal("no placement refresh after a resize")
	}
	if !m.logoImage() {
		t.Fatal("kitty images must stay on screen during a resize")
	}
	if _, cmd := m.Update(logoDraw{gen: m.logoGen - 1}); cmd != nil {
		t.Fatal("stale timer acted")
	}
}

func TestKittyInsideTmuxIsWrapped(t *testing.T) {
	if got := tmuxPassthrough("\x1b_Ga=p\x1b\\"); got != "\x1bPtmux;\x1b\x1b_Ga=p\x1b\x1b\\\x1b\\" {
		t.Fatalf("envelope %q", got)
	}
	plain := New("tester", false)
	plain.gfx = gfxKitty
	plain.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if seq := plain.logoSequence(); strings.Contains(seq, "\x1bPtmux;") {
		t.Fatal("wrapped outside tmux")
	}
	m := New("tester", false)
	m.gfx, m.tmux = gfxKitty, true
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	seq := m.logoSequence()
	// Every graphics escape is wrapped alone: none appears bare, and no
	// envelope holds more than one 4096 byte chunk.
	if strings.Count(seq, "\x1bPtmux;") != strings.Count(seq, "\x1b\x1b_G") {
		t.Fatal("a graphics escape is not wrapped")
	}
	if strings.Contains(strings.ReplaceAll(seq, "\x1b\x1b_G", ""), "\x1b_G") {
		t.Fatal("bare graphics escape inside tmux")
	}
	for env := range strings.SplitSeq(seq, "\x1bPtmux;") {
		if len(env) > 4096+64 {
			t.Fatalf("envelope of %d bytes", len(env))
		}
	}
	if !strings.Contains(seq, "a=t,f=100,i=1") || !strings.Contains(seq, "a=p,U=1") {
		t.Fatal("transmit or placement missing")
	}
}
