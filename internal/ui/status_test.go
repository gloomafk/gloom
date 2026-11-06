package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var wordChars = regexp.MustCompile(`[A-Za-z0-9]`)

func statusBar(m *Model) []string {
	lines := strings.Split(m.View().Content, "\n")
	bar := lines[len(lines)-statusRows:]
	for i := range bar {
		bar[i] = ansi.Strip(bar[i])
	}
	return bar
}

func TestStatusBarTextAlignment(t *testing.T) {
	for _, gfx := range []graphics{gfxNone, gfxKitty} {
		for _, light := range []bool{false, true} {
			for _, view := range []viewMode{viewWindow, viewConfig, viewList} {
				m := New("tester", light)
				m.gfx = gfx
				m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
				m.drawLogo()
				m.selectWindow(m.ensure("#gloom"))
				m.ensure("#rust")
				m.add(m.ensure("#rust"), "alice", "tester: hello")
				m.view = view
				bar := statusBar(m)
				for r, row := range bar {
					has := wordChars.MatchString(row)
					if r == statusTextRow && !has {
						t.Fatalf("gfx %v light %v view %v: no text on the text row: %q", gfx, light, view, bar)
					}
					if r != statusTextRow && has {
						t.Fatalf("gfx %v light %v view %v: text on row %d, want only row %d: %q", gfx, light, view, r, statusTextRow, bar)
					}
				}
				for _, want := range []string{"GLOOM"} {
					if !strings.Contains(bar[statusTextRow], want) {
						t.Fatalf("gfx %v light %v view %v: %q not on the text row: %q", gfx, light, view, want, bar[statusTextRow])
					}
				}
			}
		}
	}
}

// opaqueBounds is the box around every pixel with alpha at or above 1/4,
// ignoring rows at or below skipFrom.
func opaqueBounds(img image.Image, skipFrom int) image.Rectangle {
	b := img.Bounds()
	box := image.Rectangle{Min: b.Max, Max: b.Min}
	for y := b.Min.Y; y < min(b.Max.Y, skipFrom); y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a >= 0x4000 {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box
}

// The logo and icons are drawn over placeholder cells, so what they show is
// fixed by their pixels. Each must be centred on the text row and have the cell
// aspect, or the terminal letterboxes it and it drifts from the text.
func TestStatusBarImageAlignment(t *testing.T) {
	for _, light := range []bool{false, true} {
		m := New("tester", light)
		sprites := m.themeSprites()
		if len(sprites) == 0 {
			t.Fatal("no sprites")
		}
		for _, sp := range sprites {
			img, err := png.Decode(bytes.NewReader(sp.png()))
			if err != nil {
				t.Fatalf("%s: %v", sp.key, err)
			}
			w, h := img.Bounds().Dx(), img.Bounds().Dy()
			if w*sp.rows*cellPxH != h*sp.cols*cellPxW {
				t.Errorf("%s: canvas %dx%d does not have the aspect of %dx%d cells", sp.key, w, h, sp.cols, sp.rows)
			}
			if sp.rows != statusRows {
				t.Errorf("%s: spans %d rows, the bar has %d", sp.key, sp.rows, statusRows)
			}
			rowH := h / statusRows
			isLogo := strings.HasPrefix(sp.key, "logo")
			skip := h
			if !isLogo {
				skip = h - markerPx*rowH/cellPxH
			}
			box := opaqueBounds(img, skip)
			if box.Empty() {
				t.Errorf("%s: nothing drawn", sp.key)
				continue
			}
			top, bottom := statusTextRow*rowH, (statusTextRow+1)*rowH
			centre := (top + bottom) / 2
			// Icons fit inside the text row. The 1.2 row mark spills a tenth of a row
			// into the padding above and below, evenly.
			allowTop, allowBottom, tol := top, bottom, rowH/20
			if isLogo {
				allowTop, allowBottom = top-rowH/8, bottom+rowH/8
			}
			if box.Min.Y < allowTop || box.Max.Y > allowBottom {
				t.Errorf("%s: drawn from y %d to %d, outside %d to %d", sp.key, box.Min.Y, box.Max.Y, allowTop, allowBottom)
			}
			if d := (box.Min.Y+box.Max.Y)/2 - centre; abs(d) > tol {
				t.Errorf("%s: vertical centre is %d pixels off the text row centre (row is %d)", sp.key, d, rowH)
			}
			// The mark is centred in its 26px slot, not the canvas, which also holds
			// the padding before it and the gap after it.
			if d := (box.Min.X+box.Max.X)/2 - w/2; abs(d) > w/10 && !isLogo {
				t.Errorf("%s: horizontal centre is %d pixels off (canvas is %d wide)", sp.key, d, w)
			}
		}
	}
}

func TestStatusBarMatchesPrototype(t *testing.T) {
	near := func(what string, got, want, tol float64) {
		t.Helper()
		if got < want-tol || got > want+tol {
			t.Errorf("%s = %.3f, want %.3f (+/- %.3f)", what, got, want, tol)
		}
	}
	m := New("tester", false)
	for _, sp := range m.themeSprites() {
		img, err := png.Decode(bytes.NewReader(sp.png()))
		if err != nil {
			t.Fatal(err)
		}
		rowH := float64(img.Bounds().Dy() / statusRows)
		if strings.HasPrefix(sp.key, "logo") {
			// 26 by 24 logo: 24 / 20 = 1.2 rows tall.
			near(sp.key+" height in rows", float64(opaqueBounds(img, img.Bounds().Dy()).Dy())/rowH, protoLogoH/protoCellH, 0.05)
			continue
		}
		// 16 by 16 icon with 1.5 strokes: the box is 0.8 rows.
		box := opaqueBounds(img, img.Bounds().Dy()-markerPx)
		// The strokes run from 2 to 14 of the 16 unit box, plus the stroke width:
		// 11 to 14 px of a 20px row.
		if r := float64(box.Dy()) / rowH; r < 11/protoCellH || r > 14/protoCellH {
			t.Errorf("%s: glyph is %.3f rows tall, want %.3f to %.3f", sp.key, r, 11/protoCellH, 14/protoCellH)
		}
	}
	near("marker rows", float64(markerPx)/cellPxH, 2.0/protoCellH, 0.005)
	near("icon button width in px", iconCols*protoCellW, protoButtonW, 0.1*protoButtonW)
	near("logo slot width in px", logoCols*protoCellW, protoLogoX+protoLogoW+protoLogoGap, 0.5)
	// The prototype's bar is two rows; gloom's is three so that its content can
	// be centred on a middle row (see statusRows). The content is still 40px.
	if statusRows != 3 || statusTextRow != 1 {
		t.Errorf("status bar is %d rows with text on row %d, want 3 rows with text on row 1", statusRows, statusTextRow)
	}

	m.gfx = gfxKitty
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.drawLogo()
	m.selectWindow(m.ensure("#gloom"))
	m.add(m.ensure("#rust"), "alice", "tester: hello")
	_, hits := m.status()
	for _, h := range hits {
		switch h.index {
		case hitLogo:
			near("GLOOM segment width in px", float64(h.end-h.start)*protoCellW, protoGloomW, 0.1*protoGloomW)
		case hitChannels, hitConfig, hitDebug, hitLayout:
			near("icon button width in px", float64(h.end-h.start)*protoCellW, protoButtonW, 0.1*protoButtonW)
		}
	}
	// Tabs are padding, label, padding: the 1px rule sits in the right padding
	// rather than taking a column of its own.
	bar := statusBar(m)[statusTextRow]
	for _, want := range []string{" #gloom▕", " #rust 1▕"} {
		if !strings.Contains(bar, want) {
			t.Errorf("tab %q not found in %q", want, bar)
		}
	}
	if strings.Contains(bar, "│") {
		t.Errorf("bar draws a full-column rule, want 1/8 column rules: %q", bar)
	}
}

func TestOpenMarkerClearsGlyph(t *testing.T) {
	for _, name := range []string{"channels", "config", "debug", "layout"} {
		img, err := png.Decode(bytes.NewReader(renderIcon(name, "#5E9BFF", layoutChannels|layoutNicks|layoutStatus, "#5E9BFF")))
		if err != nil {
			t.Fatal(err)
		}
		h := img.Bounds().Dy()
		glyph := opaqueBounds(img, h-markerPx)
		if glyph.Max.Y >= h-markerPx {
			t.Errorf("%s: glyph reaches y %d, marker starts at %d", name, glyph.Max.Y, h-markerPx)
		}
		if _, _, _, a := img.At(0, h-1).RGBA(); a == 0 {
			t.Errorf("%s: no marker on the bottom row", name)
		}
	}
}

func abs(n int) int { return max(n, -n) }

func TestDividerSitsBetweenInputAndStatusBar(t *testing.T) {
	for _, gfx := range []graphics{gfxNone, gfxKitty} {
		m := New("tester", false)
		m.gfx = gfx
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		if len(lines) != 30 {
			t.Fatalf("%d lines, want 30", len(lines))
		}
		div := lines[len(lines)-statusRows-1]
		if strings.TrimSpace(div) != "" {
			t.Errorf("gfx %d: row above the bar is %q, want it blank, with no rule", gfx, div)
		}
		if !strings.HasPrefix(lines[len(lines)-bottomRows-1], " ❯") {
			t.Errorf("gfx %d: input row is %q, want it above the rule", gfx, lines[len(lines)-bottomRows-1])
		}
		for r, l := range lines[len(lines)-statusRows:] {
			if strings.Contains(l, "▁") || strings.Contains(l, "▔") {
				t.Errorf("gfx %d: a rule was drawn in status row %d", gfx, r)
			}
		}
		if pad := lines[len(lines)-bottomRows-2]; strings.TrimSpace(ansi.Strip(pad)) != "" {
			t.Errorf("gfx %d: row above the input is %q, want a blank padding row", gfx, ansi.Strip(pad))
		}
		if m.body.h != 30-2-inputPadRows-bottomRows {
			t.Errorf("message area is %d rows, want %d", m.body.h, 30-2-inputPadRows-bottomRows)
		}
	}
}

func TestNicklistRunsDownToStatusBar(t *testing.T) {
	m := sized(100, 30)
	m.nicklist = true
	for i := range 40 {
		m.windows[m.active].nicks[fmt.Sprintf("nick%02d", i)] = fmt.Sprintf("nick%02d", i)
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if m.paneR == 0 {
		t.Fatal("nicklist not shown")
	}
	inputY := len(lines) - bottomRows - 1
	if !strings.HasPrefix(lines[inputY], " ❯") {
		t.Fatalf("input row is %q", lines[inputY])
	}
	// Every row from the topic bar down to the bar carries the nicklist, and the
	// rows beside it stop at its edge.
	for y := 0; y < len(lines)-statusRows; y++ {
		side := ansi.Cut(lines[y], m.width-m.paneR, m.width)
		if !strings.Contains(side, "nick") && !strings.Contains(side, "NICKS") && !strings.Contains(side, "tester") {
			t.Errorf("row %d has no nicklist entry at its right edge: %q", y, lines[y])
		}
		if w := ansi.StringWidth(lines[y]); w != m.width {
			t.Errorf("row %d is %d wide, want %d", y, w, m.width)
		}
	}
	for _, l := range lines[len(lines)-statusRows:] {
		if strings.Contains(l, "nick") {
			t.Errorf("nicklist leaked into the status bar: %q", l)
		}
	}
	if !m.grab(m.width-m.paneR, inputY) {
		t.Error("the nicklist border beside the input row cannot be dragged")
	}
	m.drag = paneNone
	if m.grab(m.width-m.paneR, len(lines)-statusRows) {
		t.Error("the status bar starts a nicklist drag")
	}
}

func TestWindowListRunsDownToStatusBar(t *testing.T) {
	m := sized(100, 30)
	m.channels, m.nicklist = true, false
	for i := range 30 {
		m.ensure(fmt.Sprintf("#chan%02d", i))
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if m.paneL == 0 {
		t.Fatal("window list not shown")
	}
	inputY := len(lines) - bottomRows - 1
	if got := ansi.Cut(lines[inputY], m.paneL, m.paneL+2); got != " ❯" {
		t.Fatalf("input row is %q, want the prompt right of the window list", lines[inputY])
	}
	for y := 0; y < len(lines)-statusRows; y++ {
		if edge := ansi.Cut(lines[y], m.paneL-1, m.paneL); edge != "│" {
			t.Errorf("row %d has no window list border: %q", y, lines[y])
		}
		if w := ansi.StringWidth(lines[y]); w != m.width {
			t.Errorf("row %d is %d wide, want %d", y, w, m.width)
		}
	}
	for _, l := range lines[len(lines)-statusRows:] {
		if strings.Contains(l, "│") || strings.Contains(l, "#chan2") {
			t.Errorf("window list leaked into the status bar: %q", l)
		}
	}
	clickAt(m, 2, inputY, tea.MouseLeft)
	if m.active != inputY-1 {
		t.Errorf("clicking the window list beside the input selected window %d, want %d", m.active, inputY-1)
	}
	if !m.grab(m.paneL-1, inputY) {
		t.Error("the window list border beside the input row cannot be dragged")
	}
	m.drag = paneNone
	if m.grab(m.paneL-1, len(lines)-statusRows) {
		t.Error("the status bar starts a window list drag")
	}
}

func TestSidePanesShareAQuieterBorder(t *testing.T) {
	for _, light := range []bool{false, true} {
		m := sized(200, 30)
		m.light = light
		m.channels, m.nicklist = true, true
		p := m.colors()
		if p.edge == p.faint || p.edge == "" {
			t.Fatalf("light %v: edge %q must be its own colour, not faint %q", light, p.edge, p.faint)
		}
		// Quieter: closer to the background the panes sit on than faint is.
		if lumaGap(p.edge, p.bg) >= lumaGap(p.faint, p.bg) {
			t.Errorf("light %v: edge %s is not quieter than faint %s on %s", light, p.edge, p.faint, p.bg)
		}
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		if m.paneL == 0 || m.paneR == 0 {
			t.Fatal("both panes should be shown")
		}
		for y := 0; y < len(lines)-statusRows; y++ {
			if got := ansi.Cut(lines[y], m.paneL-1, m.paneL); got != "│" {
				t.Errorf("light %v row %d: no windows pane border: %q", light, y, lines[y])
			}
			if got := ansi.Cut(lines[y], m.width-m.paneR, m.width-m.paneR+1); got != "│" {
				t.Errorf("light %v row %d: no nicklist border: %q", light, y, lines[y])
			}
		}
	}
}

// lumaGap is the difference in perceived brightness of two #RRGGBB colours.
func lumaGap(a, b string) float64 {
	luma := func(c string) float64 {
		var r, g, bl int
		fmt.Sscanf(c, "#%02x%02x%02x", &r, &g, &bl)
		return 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(bl)
	}
	return math.Abs(luma(a) - luma(b))
}

func TestSidePanesUseChatBackground(t *testing.T) {
	m := sized(200, 30)
	m.channels, m.nicklist = true, true
	m.ensure("#other")
	m.View()
	p := m.colors()
	bgSeq := func(hex string) string {
		var r, g, b int
		fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
		return fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
	}
	if m.nlCols != nicklistW || m.clCols != chanlistW || nicklistW != 32 || chanlistW != 32 {
		t.Fatalf("pane widths %d and %d, want the defaults 32 and 32", m.clCols, m.nlCols)
	}
	nicks, _ := m.nickRows(m.windows[m.active], p, 20)
	for i, r := range nicks {
		if strings.Contains(r, bgSeq(p.panel)) {
			t.Errorf("nicklist row %d is drawn on the panel colour: %q", i, r)
		}
	}
	for i, r := range m.chanlistRows(p, 20) {
		selected := i == m.active+1 // row 0 is the WINDOWS title
		if got := strings.Contains(r, bgSeq(p.panel)); got != selected {
			t.Errorf("windows row %d: panel colour %v, want %v (only the selected window is tinted)", i, got, selected)
		}
	}
}

func TestTopicBarSitsBetweenSidePanes(t *testing.T) {
	m := sized(200, 30)
	m.channels, m.nicklist = true, true
	top := ansi.Strip(strings.Split(m.View().Content, "\n")[0])
	if m.paneL == 0 || m.paneR == 0 {
		t.Fatal("both panes should be shown")
	}
	if got := ansi.Cut(top, 0, m.paneL); !strings.HasPrefix(got, " WINDOWS") || !strings.HasSuffix(got, "│") {
		t.Errorf("top row left of the topic bar is %q, want the WINDOWS title and border", got)
	}
	if got := ansi.Cut(top, m.width-m.paneR, m.width); !strings.HasPrefix(got, "│ NICKS") {
		t.Errorf("top row right of the topic bar is %q, want the nicklist border and NICKS title", got)
	}
	topic := ansi.Cut(top, m.paneL, m.width-m.paneR)
	if !strings.HasPrefix(topic, " #gloom") || !strings.HasSuffix(topic, "nicks ") {
		t.Errorf("topic bar is %q, want it to start right of the windows pane and end left of the nicklist", topic)
	}
	clickAt(m, 2, 1, tea.MouseLeft)
	if m.active != 0 {
		t.Errorf("clicking row 1 of the window list selected window %d, want 0", m.active)
	}
}
