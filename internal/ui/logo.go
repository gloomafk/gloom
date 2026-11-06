package ui

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/iterm2"
)

// The dark accent foreground uses the black mark; the light theme uses white.
//
//go:embed assets/gloom-mark-black.png
var markBlackPNG []byte

//go:embed assets/gloom-mark-white.png
var markWhitePNG []byte

type graphics int

const (
	gfxNone graphics = iota
	gfxKitty
	gfxITerm2
)

// logoScale is canvas pixels per cell pixel for the logo. The mark is scaled
// down onto the canvas, so a small canvas keeps the PNG light.
const logoScale = 2

// detectGraphics picks an image protocol from the environment. GLOOM_GRAPHICS
// (off, kitty or iterm2) overrides detection, except in Terminal.app, which
// supports neither protocol and would print the image data as text.
// Multiplexers swallow the escapes, so they select the text fallback.
func detectGraphics(env func(string) string) graphics {
	if env("TERM_PROGRAM") == "Apple_Terminal" {
		return gfxNone
	}
	switch env("GLOOM_GRAPHICS") {
	case "off":
		return gfxNone
	case "kitty":
		return gfxKitty
	case "iterm2":
		return gfxITerm2
	}
	if env("TMUX") != "" || env("STY") != "" {
		return gfxNone
	}
	switch {
	case env("KITTY_WINDOW_ID") != "", env("TERM") == "xterm-kitty", env("TERM") == "xterm-ghostty",
		env("TERM_PROGRAM") == "ghostty", env("TERM_PROGRAM") == "WezTerm":
		return gfxKitty
	case env("TERM_PROGRAM") == "iTerm.app":
		return gfxITerm2
	}
	return gfxNone
}

type spritesReady map[string][]byte

// logoDraw fires after a quiet period. gen discards timers from earlier resizes.
type logoDraw struct{ gen int }

// placement is one image. col and row are 1-based cells, used by iTerm2 only.
type placement struct {
	key                  string
	png                  func() []byte
	col, row, cols, rows int
}
type sprite struct {
	id   int
	png  []byte
	sent bool
}

func (m *Model) imagesWanted() bool { return m.gfx != gfxNone && m.fitsLayout() }

// logoImage reports whether the bar is laid out for images. On Kitty it is from
// the first frame, with blank cells until the images arrive, so the bar never
// changes shape. Elsewhere, until the images are drawn, and after a resize on
// terminals that paint images into cells, the icons show as text glyphs.
func (m *Model) logoImage() bool {
	return m.imagesWanted() && (m.gfx == gfxKitty || m.imagesUp)
}

// Encode every variant off the UI loop so startup and view toggles stay responsive.
func (m *Model) renderSprites() tea.Cmd {
	ps := m.themeSprites()
	m.pngsPending = true
	return func() tea.Msg {
		encoded := make([][]byte, len(ps))
		var wg sync.WaitGroup
		for i, p := range ps {
			wg.Go(func() { encoded[i] = p.png() })
		}
		wg.Wait()
		ready := make(spritesReady, len(ps))
		for i, p := range ps {
			ready[p.key] = encoded[i]
		}
		return ready
	}
}

func logoPNG(light bool) []byte {
	data := markBlackPNG
	if light {
		data = markWhitePNG
	}
	mark, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, logoCols*cellPxW*logoScale, statusRows*cellPxH*logoScale))
	rowH := cellPxH * logoScale
	b := mark.Bounds()
	h := int(logoRows * float64(rowH))
	w := h * b.Dx() / b.Dy()
	small := shrink(mark, w, h)
	// The mark keeps its own aspect inside the prototype's 26px wide slot, which
	// starts 8px in: centre it on the slot.
	slot := (protoLogoX + protoLogoW/2) / protoCellW * cellPxW * logoScale
	at := image.Pt(int(slot)-w/2, statusTextRow*rowH+(rowH-h)/2)
	draw.Draw(canvas, image.Rectangle{at, at.Add(small.Bounds().Size())}, small, image.Point{}, draw.Src)
	var buf bytes.Buffer
	_ = png.Encode(&buf, canvas)
	return buf.Bytes()
}

// shrink scales src down to w by h by averaging the source pixels under each
// destination pixel, weighting colour by alpha so edges do not darken.
func shrink(src image.Image, w, h int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := b.Min.Y+y*b.Dy()/h, max(b.Min.Y+(y+1)*b.Dy()/h, b.Min.Y+y*b.Dy()/h+1)
		for x := range w {
			x0, x1 := b.Min.X+x*b.Dx()/w, max(b.Min.X+(x+1)*b.Dx()/w, b.Min.X+x*b.Dx()/w+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					// RGBA is premultiplied, so summing it already weights by alpha.
					pr, pg, pb, pa := src.At(sx, sy).RGBA()
					r, g, bl, a, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa), n+1
				}
			}
			if a == 0 {
				continue
			}
			dst.SetNRGBA(x, y, color.NRGBA{uint8(r * 255 / a), uint8(g * 255 / a), uint8(bl * 255 / a), uint8(a / n >> 8)})
		}
	}
	return dst
}
func logoKey(light bool) string {
	if light {
		return "logo-light"
	}
	return "logo-dark"
}

func (ic iconSpec) render(p palette) []byte {
	underline := ""
	if ic.on {
		underline = p.accent
	}
	return renderIcon(ic.name, ic.fg, ic.flags, underline)
}
func iconKey(ic iconSpec) string {
	return fmt.Sprintf("icon-%s-%s-%d-%t", ic.name, ic.fg, ic.flags, ic.on)
}

// themeSprites lists every image the status bar can need in the current theme,
// so toggling a view or a layout never has to send an image.
func (m *Model) themeSprites() []placement {
	p := m.colors()
	out := []placement{logoPlacement(m.light)}
	for _, ic := range m.iconVariants(p) {
		out = append(out, iconPlacement(ic, p))
	}
	return out
}

func logoPlacement(light bool) placement {
	return placement{key: logoKey(light), png: func() []byte { return logoPNG(light) }, cols: logoCols, rows: statusRows}
}

func iconPlacement(ic iconSpec, p palette) placement {
	return placement{key: iconKey(ic), png: func() []byte { return ic.render(p) }, cols: iconCols, rows: statusRows}
}

// placements are the images drawn over cells by iTerm2, which has no placeholders.
func (m *Model) placements() []placement {
	if !m.imagesWanted() {
		return nil
	}
	top := m.height - statusRows + 1
	p := m.colors()
	logo := logoPlacement(m.light)
	logo.col, logo.row = 1, top
	ps := []placement{logo}
	icons := m.icons(p)
	for i, ic := range icons {
		icon := iconPlacement(ic, p)
		icon.col, icon.row = m.width-(len(icons)-i)*iconCols+1, top
		ps = append(ps, icon)
	}
	return ps
}
func (m *Model) logoSig() string {
	if m.gfx == gfxKitty {
		return fmt.Sprint(m.light, m.imagesWanted())
	}
	var s strings.Builder
	for _, p := range m.placements() {
		fmt.Fprint(&s, p.key, p.col, p.row, ";")
	}
	return s.String()
}

func (m *Model) themeReady() bool {
	for _, p := range m.themeSprites() {
		if _, ready := m.pngs[p.key]; ready {
			continue
		}
		if _, cached := m.sprites[p.key]; !cached {
			return false
		}
	}
	return true
}

// scheduleLogo queues image preparation or a redraw after the frame is painted.
func (m *Model) scheduleLogo() tea.Cmd {
	if m.imagesWanted() && !m.themeReady() {
		if m.pngsPending {
			return nil
		}
		return m.renderSprites()
	}
	if m.gfx == gfxNone || m.logoPending || m.logoSig() == m.logoDrawn {
		return nil
	}
	// Nothing is on screen yet, so there is no repaint to wait out: draw as soon
	// as the images are encoded.
	if m.gfx == gfxKitty && !m.logoSent {
		if m.pngsPending {
			return nil
		}
		if m.pngs != nil {
			return m.drawLogo()
		}
	}
	m.logoPending = true
	gen := m.logoGen
	return tea.Tick(60*time.Millisecond, func(time.Time) tea.Msg { return logoDraw{gen} })
}

// resetLogo runs on every resize. Kitty images live in text cells and survive
// repaints, so only the cheap placements are sent again, once the size settles.
// iTerm2 paints images into cells, which a repaint overwrites, so it shows text
// until they are drawn again.
func (m *Model) resetLogo() {
	m.logoGen++
	m.logoPending = false
	m.logoDrawn = ""
	if m.gfx == gfxITerm2 {
		m.imagesUp = false
	}
}
func (m *Model) drawLogo() tea.Cmd {
	m.logoPending = false
	sig := m.logoSig()
	if sig == m.logoDrawn {
		return nil
	}
	m.logoDrawn = sig
	seq := m.logoSequence()
	m.imagesUp = m.imagesWanted()
	m.logoSent = m.logoSent || m.imagesUp
	return tea.Raw(seq)
}

func (m *Model) spriteFor(p placement) *sprite {
	if s, ok := m.sprites[p.key]; ok {
		return s
	}
	data, ok := m.pngs[p.key]
	if !ok {
		data = p.png()
	}
	s := &sprite{id: len(m.sprites) + 1, png: data}
	m.sprites[p.key] = s
	return s
}

// placeholderID returns the image id to draw a status bar element with, once
// the terminal holds that image.
func (m *Model) placeholderID(key string) (int, bool) {
	if m.gfx != gfxKitty || !m.logoImage() {
		return 0, false
	}
	s, ok := m.sprites[key]
	if !ok || !s.sent {
		return 0, false
	}
	return s.id, true
}
func (m *Model) logoSequence() string {
	if !m.imagesWanted() {
		return ""
	}
	var s strings.Builder
	switch m.gfx {
	case gfxKitty:
		for _, p := range m.themeSprites() {
			sp := m.spriteFor(p)
			if !sp.sent {
				sp.sent = true
				s.WriteString(kittyTransmit(sp.id, sp.png, m.passthrough))
			}
			// A virtual placement: cells that hold placeholder characters show the image.
			s.WriteString(m.passthrough(ansi.KittyGraphics(nil, "a=p", "U=1", fmt.Sprintf("i=%d", sp.id), "p=1",
				fmt.Sprintf("c=%d", p.cols), fmt.Sprintf("r=%d", p.rows), "q=2")))
		}
	case gfxITerm2:
		s.WriteString("\x1b7")
		for _, p := range m.placements() {
			sp := m.spriteFor(p)
			fmt.Fprintf(&s, "\x1b[%d;%dH", p.row, p.col)
			s.WriteString(ansi.ITerm2(iterm2.File{
				Inline: true, Width: iterm2.Cells(p.cols), Height: iterm2.Cells(p.rows),
				IgnoreAspectRatio: true, DoNotMoveCursor: true,
				Content: []byte(base64.StdEncoding.EncodeToString(sp.png)),
			}))
		}
		s.WriteString("\x1b8")
	}
	return s.String()
}

// passthrough wraps a graphics escape so that tmux forwards it to the outer
// terminal. Outside tmux it returns seq unchanged. tmux needs
// "set -g allow-passthrough on" (3.3 or later) or it drops the escape.
func (m *Model) passthrough(seq string) string {
	if !m.tmux {
		return seq
	}
	return tmuxPassthrough(seq)
}

// tmuxPassthrough is tmux's DCS envelope: "ESC P tmux ;", the payload with
// every ESC doubled, then ST. Each escape is wrapped alone, so no single DCS
// grows past one 4096 byte graphics chunk.
func tmuxPassthrough(seq string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// placeholder is the Kitty graphics placeholder character. Two combining marks
// after it give the row and column of the image cell it stands for.
const placeholder = "\U0010EEEE"

// diacritics are the first entries of Kitty's row and column table.
var diacritics = []rune{0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F}

// placeholderRow draws one row of an image cols cells wide. The foreground
// colour carries the image id and the underline colour the placement id.
func placeholderRow(id, row, cols int, bg string) string {
	r, g, b := hexRGB(bg)
	var s strings.Builder
	fmt.Fprintf(&s, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%d;58;2;0;0;1m", id>>16&255, id>>8&255, id&255, r, g, b)
	for c := range cols {
		s.WriteString(placeholder)
		s.WriteRune(diacritics[row])
		s.WriteRune(diacritics[c])
	}
	s.WriteString("\x1b[m")
	return s.String()
}

// kittyTransmit uploads a PNG without displaying it, in chunks of at most 4096
// bytes. wrap is applied to each chunk's escape.
func kittyTransmit(id int, png []byte, wrap func(string) string) string {
	enc := base64.StdEncoding.EncodeToString(png)
	var s strings.Builder
	for first := true; len(enc) > 0; first = false {
		n := min(4096, len(enc))
		more := "m=0"
		if n < len(enc) {
			more = "m=1"
		}
		opts := []string{more}
		if first {
			opts = []string{"a=t", "f=100", fmt.Sprintf("i=%d", id), "q=2", more}
		}
		s.WriteString(wrap(ansi.KittyGraphics([]byte(enc[:n]), opts...)))
		enc = enc[n:]
	}
	return s.String()
}
