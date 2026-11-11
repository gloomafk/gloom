package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSlitGhostLogo(t *testing.T) {
	// The supplied 12e master artwork, one character per square pixel cell.
	// Spaces include the two transparent vertical eyes and the trailing tail.
	shape := []string{
		"  #####   ",
		" #######  ",
		"######### ",
		"## ### ## ",
		"## ### ## ",
		"######### ",
		"######### ",
		"######### ",
		" ### #####",
	}
	for _, light := range []bool{false, true} {
		t.Run(fmt.Sprintf("light=%t", light), func(t *testing.T) {
			data := markBlackPNG
			ink := color.NRGBA{0, 0, 0, 255}
			if light {
				data, ink = markBonePNG, color.NRGBA{242, 242, 239, 255}
			}
			source, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if source.Bounds() != image.Rect(0, 0, 10, 9) {
				t.Fatalf("source bounds = %v, want the unpadded 10 by 9 master", source.Bounds())
			}
			for y, row := range shape {
				for x, cell := range row {
					want := color.NRGBA{}
					if cell == '#' {
						want = ink
					}
					if got := color.NRGBAModel.Convert(source.At(x, y)); got != want {
						t.Fatalf("source pixel (%d,%d) = %v, want %v", x, y, got, want)
					}
				}
			}
			img, err := png.Decode(bytes.NewReader(logoPNG(light)))
			if err != nil {
				t.Fatal(err)
			}
			if got := img.Bounds(); got != image.Rect(0, 0, 550, 720) {
				t.Fatalf("canvas = %v, want 550 by 720", got)
			}
			nrgba, ok := img.(*image.NRGBA)
			if !ok {
				t.Fatalf("canvas decoded as %T, want *image.NRGBA", img)
			}
			// 24/20 rows at 240px per row gives 288px, or 32px per cell.
			// The slot centre is (8+26/2)/7.8 columns at 110px per column.
			box := image.Rect(136, 216, 456, 504)
			for y := range 720 {
				for x := range 550 {
					want := color.NRGBA{}
					if image.Pt(x, y).In(box) && shape[(y-box.Min.Y)/32][(x-box.Min.X)/32] == '#' {
						want = ink
					}
					if got := nrgba.NRGBAAt(x, y); got != want {
						t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
					}
				}
			}
		})
	}
}

func TestDamagedLogoKeepsCanvasGeometry(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not a PNG"), markBlackPNG[:len(markBlackPNG)/2]} {
		img, err := png.Decode(bytes.NewReader(renderLogoPNG(data)))
		if err != nil {
			t.Fatalf("fallback is not a PNG: %v", err)
		}
		if img.Bounds() != image.Rect(0, 0, 550, 720) {
			t.Fatalf("fallback has wrong geometry: %v", img.Bounds())
		}
		if box := opaqueBounds(img, img.Bounds().Max.Y); !box.Empty() {
			t.Fatalf("damaged logo drew pixels: %v", box)
		}
	}
}

func TestLogoScalesUnpaddedExports(t *testing.T) {
	source, err := png.Decode(bytes.NewReader(markBlackPNG))
	if err != nil {
		t.Fatal(err)
	}
	want := logoPNG(false)
	for _, scale := range []int{2, 32} {
		larger := image.NewNRGBA(image.Rect(0, 0, 10*scale, 9*scale))
		for y := range larger.Bounds().Dy() {
			for x := range larger.Bounds().Dx() {
				larger.Set(x, y, source.At(x/scale, y/scale))
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, larger); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(renderLogoPNG(buf.Bytes()), want) {
			t.Fatalf("%dx export changed rendered logo", scale)
		}
	}
}

func TestLogoPNGAllocations(t *testing.T) {
	// PNG encoding has a small fixed cost; sampling must not allocate per pixel.
	allocs := testing.AllocsPerRun(5, func() { logoPNG(false) })
	t.Logf("logoPNG allocations: %.0f", allocs)
	if allocs > 1000 {
		t.Fatalf("logoPNG allocates %.0f times; want fewer than 1000", allocs)
	}
}

func TestLogoLabelMatchesMarkColour(t *testing.T) {
	for _, tc := range []struct {
		light bool
		fg    string
	}{{false, "#000000"}, {true, "#F2F2EF"}} {
		for _, gfx := range []graphics{gfxNone, gfxKitty} {
			m := New("tester", tc.light)
			m.gfx = gfx
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			m.drawLogo()
			rows, _ := m.status()
			label := " GLOOM "
			if gfx == gfxKitty {
				label = "GLOOM "
			}
			want := style(tc.fg, m.colors().accent).Bold(true).Render(label)
			if !strings.Contains(rows[statusTextRow], want) {
				t.Fatalf("light=%t gfx=%v: label does not use %s", tc.light, gfx, tc.fg)
			}
		}
	}
}

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
		// Run only image preparation; leave the recurring Init timers idle.
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
		_, upload = m.Update(tea.ModeReportMsg{Mode: ansi.ModeAltScreenSaveCursor, Value: ansi.ModeSet})
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

func TestStartupSmallPaneDoesNotCompleteImageStartup(t *testing.T) {
	m := New("tester", false)
	m.gfx = gfxKitty
	m.Init()
	m.Update(tea.ModeReportMsg{Mode: ansi.ModeAltScreenSaveCursor, Value: ansi.ModeSet})
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	m.Update(m.renderSprites()())
	if m.scheduleLogo() != nil || m.drawLogo() != nil {
		t.Fatal("small pane scheduled graphics output")
	}
	m.Update(tea.Raw(imageOutput(""))())
	if m.imagesQueued || !m.starting {
		t.Fatal("empty output completed startup")
	}
	if m.View().Content == "" || !m.starting {
		t.Fatal("small-pane view changed lifecycle state or hid warning")
	}
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
	if m.View().Content != "" || !m.starting {
		t.Fatal("growing pane exposed an incomplete bar")
	}
	m.Update(ghostTick{})
	if m.ghost.frame != 0 {
		t.Fatal("hidden animation advanced")
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
