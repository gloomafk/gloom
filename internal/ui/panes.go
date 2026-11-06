package ui

import (
	"fmt"
	"time"
)

type pane int

const (
	paneNone pane = iota
	paneChanlist
	paneNicklist
)

const (
	minPane   = 12
	maxPane   = 72
	gripDelay = 400 * time.Millisecond
)

func clampPane(cols int) int { return max(minPane, min(maxPane, cols)) }

func dragBadge(p palette, cols int) string {
	return style(p.accentFg, p.accent).Bold(true).Render(fmt.Sprintf(" %d cols ", cols))
}

// grab starts dragging a pane when the press lands on its border: the last
// column of the windows pane, or the first column of the nicklist. A second
// press within gripDelay resets the pane to its default width.
func (m *Model) grab(x, y int) bool {
	if y < 0 || y >= m.height-statusRows {
		return false
	}
	var which pane
	switch {
	case m.paneL > 0 && x == m.paneL-1:
		which = paneChanlist
	case m.paneR > 0 && x == m.width-m.paneR:
		which = paneNicklist
	default:
		return false
	}
	now := time.Now()
	if m.lastGrip == which && now.Sub(m.gripAt) < gripDelay {
		if which == paneChanlist {
			m.clCols = chanlistW
		} else {
			m.nlCols = nicklistW
		}
		m.drag, m.lastGrip = paneNone, paneNone
		return true
	}
	m.drag, m.lastGrip, m.gripAt = which, which, now
	return true
}

func (m *Model) dragTo(x int) {
	switch m.drag {
	case paneChanlist:
		m.clCols = clampPane(x + 1)
	case paneNicklist:
		m.nlCols = clampPane(m.width - x)
	}
}
