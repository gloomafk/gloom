package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
)

// Icons use a 16 unit viewBox, stroke 1.5 and round caps. Each is rasterized
// with a small distance-field renderer so no SVG library is needed.

type vec struct{ x, y float64 }

// stroke reports the distance from p to a shape's centre line.
type stroke func(p vec) float64

func seg(x1, y1, x2, y2 float64) stroke {
	a, b := vec{x1, y1}, vec{x2, y2}
	return func(p vec) float64 {
		ab, ap := vec{b.x - a.x, b.y - a.y}, vec{p.x - a.x, p.y - a.y}
		t := max(0, min(1, (ap.x*ab.x+ap.y*ab.y)/(ab.x*ab.x+ab.y*ab.y)))
		return math.Hypot(ap.x-t*ab.x, ap.y-t*ab.y)
	}
}
func circle(cx, cy, r float64) stroke {
	return func(p vec) float64 { return math.Abs(math.Hypot(p.x-cx, p.y-cy) - r) }
}

func topArc(cx, cy, r float64) stroke {
	ring := circle(cx, cy, r)
	return func(p vec) float64 {
		if p.y > cy {
			return math.Inf(1)
		}
		return ring(p)
	}
}
func rrect(x, y, w, h, r float64) stroke {
	return func(p vec) float64 {
		qx, qy := math.Abs(p.x-(x+w/2))-(w/2-r), math.Abs(p.y-(y+h/2))-(h/2-r)
		d := math.Hypot(max(qx, 0), max(qy, 0)) + min(max(qx, qy), 0) - r
		return math.Abs(d)
	}
}

const (
	layoutChannels = 1 << iota
	layoutNicks
	layoutStatus
)

func iconStrokes(name string, flags int) []stroke {
	switch name {
	case "channels":
		return []stroke{seg(6.2, 2.5, 5, 13.5), seg(11, 2.5, 9.8, 13.5), seg(2.5, 6, 13.5, 6), seg(2, 10, 13, 10)}
	case "config":
		return []stroke{seg(2, 4.5, 8.5, 4.5), seg(12.5, 4.5, 14, 4.5), circle(10.5, 4.5, 1.8),
			seg(2, 11.5, 4, 11.5), seg(8, 11.5, 14, 11.5), circle(6, 11.5, 1.8)}
	case "debug":
		return []stroke{topArc(8, 5.2, 2.4), rrect(4.6, 5.2, 6.8, 8.3, 3.4), seg(8, 8, 8, 13.5),
			seg(1.8, 8.4, 4.6, 8.4), seg(11.4, 8.4, 14.2, 8.4), seg(2.4, 12.6, 4.8, 11.4),
			seg(13.6, 12.6, 11.2, 11.4), seg(2.8, 4.4, 4.8, 5.8), seg(13.2, 4.4, 11.2, 5.8)}
	case "layout":
		s := []stroke{rrect(2, 2.5, 12, 11, 1)}
		if flags&layoutStatus != 0 {
			s = append(s, seg(2, 10.5, 14, 10.5))
		}
		if flags&layoutChannels != 0 {
			s = append(s, seg(6, 2.5, 6, 13.5))
		}
		if flags&layoutNicks != 0 {
			s = append(s, seg(10, 2.5, 10, 13.5))
		}
		return s
	}
	return nil
}

// Icon canvases are iconCols by statusRows cells. The glyph is the prototype's
// 16px box, iconBoxRows of a row, centred on statusTextRow so it lines up with
// the bar's text.

func renderIcon(name, hex string, flags int, underline string) []byte {
	const hw, aa = 0.75, 4
	w, h := iconCols*cellPxW, statusRows*cellPxH
	size := float64(cellPxH) * iconBoxRows
	ox, oy := (float64(w)-size)/2, float64(statusTextRow*cellPxH)+(float64(cellPxH)-size)/2
	r, g, b := hexRGB(hex)
	strokes := iconStrokes(name, flags)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := int(oy); y < int(oy+size); y++ {
		for x := int(ox); x < int(ox+size); x++ {
			covered := 0
			for sy := range aa {
				for sx := range aa {
					p := vec{(float64(x) + (float64(sx)+0.5)/aa - ox) / size * protoIcon, (float64(y) + (float64(sy)+0.5)/aa - oy) / size * protoIcon}
					for _, s := range strokes {
						if s(p) <= hw {
							covered++
							break
						}
					}
				}
			}
			if covered > 0 {
				img.SetNRGBA(x, y, color.NRGBA{r, g, b, uint8(255 * covered / (aa * aa))})
			}
		}
	}
	if underline != "" {
		// The open-view marker is the prototype's 2px inset line on the bar's bottom
		// edge. The glyph box ends 0.1 row above it, so they never touch.
		ur, ug, ub := hexRGB(underline)
		for y := h - markerPx; y < h; y++ {
			for x := range w {
				img.SetNRGBA(x, y, color.NRGBA{ur, ug, ub, 255})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
func hexRGB(s string) (r, g, b uint8) {
	v, _ := strconv.ParseUint(s[1:], 16, 32)
	return uint8(v >> 16), uint8(v >> 8), uint8(v)
}
