package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// point uses wrapped content rows, which stay fixed while scrolling,
// and columns relative to the message area.
type point struct{ row, col int }

// Mouse reporting disables terminal selection; gloom selects and copies instead.
type selection struct {
	win         *window
	sig         layoutSig
	anchor, end point
	down, moved bool
	nick        *nickHit // nick under the press, opened on release if nothing was dragged
}

// bodyGeometry is where View last drew the message area.
type bodyGeometry struct {
	x, w, h, start int
	rows           []string
}

// layoutSig names the layout a selection was made in. Anything that moves or
// rewraps the rows invalidates it.
type layoutSig struct {
	width, height, bodyX, bodyW int
	timestamps                  bool
	view                        viewMode
	win                         *window
}

func (m *Model) selSig() layoutSig {
	return layoutSig{m.width, m.height, m.body.x, m.body.w, m.timestamps, m.view, m.current()}
}

func (m *Model) selecting() bool { return m.view == viewWindow && len(m.body.rows) > 0 }

// bodyPoint maps a screen cell to the nearest cell in the message area.
func (m *Model) bodyPoint(x, y int) point {
	row := min(m.body.start+max(0, min(y-1, m.body.h-1)), len(m.body.rows)-1)
	return point{max(0, row), max(0, min(x-m.body.x, m.body.w-1))}
}

func (m *Model) beginSelect(x, y int) bool {
	m.sel = selection{}
	if !m.selecting() || y < 1 || y > m.body.h || x < m.body.x || x >= m.body.x+m.body.w {
		return false
	}
	p := m.bodyPoint(x, y)
	m.sel = selection{win: m.current(), sig: m.selSig(), anchor: p, end: p, down: true}
	m.sel.nick = m.nickAt(x, y)
	return true
}

func (m *Model) extendSelect(x, y int) {
	if !m.sel.down || !m.selecting() {
		return
	}
	switch {
	case y < 1:
		m.scroll(1)
	case y > m.body.h:
		m.scroll(-1)
	}
	m.sel.end = m.bodyPoint(x, y)
	m.sel.moved = m.sel.moved || m.sel.end != m.sel.anchor
}

// endSelect finishes a press. A click that did not move opens the nick under
// it; a drag copies what it covered.
func (m *Model) endSelect() tea.Cmd {
	if !m.sel.down {
		return nil
	}
	m.sel.down = false
	if !m.sel.moved {
		nick := m.sel.nick
		m.sel = selection{}
		if nick != nil {
			m.selectWindow(m.ensure(nick.nick))
			m.nickHits = nil
		}
		return nil
	}
	if text := m.selectedText(); text != "" {
		return tea.SetClipboard(text)
	}
	return nil
}

// selRange returns the selection in reading order, if one is showing.
func (m *Model) selRange() (a, b point, ok bool) {
	if m.sel.win == nil || !m.sel.moved || !m.selecting() || m.sel.win != m.current() {
		return point{}, point{}, false
	}
	a, b = m.sel.anchor, m.sel.end
	if b.row < a.row || (b.row == a.row && b.col < a.col) {
		a, b = b, a
	}
	return a, b, true
}

// cols is the span of row r that the selection covers, in columns of its text.
func cols(a, b point, r int, plain string) (c0, c1 int) {
	c1 = ansi.StringWidth(strings.TrimRight(plain, " "))
	if r == a.row {
		c0 = a.col
	}
	if r == b.row {
		c1 = min(c1, b.col+1)
	}
	return c0, c1
}

func (m *Model) selectedText() string {
	a, b, ok := m.selRange()
	if !ok {
		return ""
	}
	var lines []string
	for r := a.row; r <= b.row && r < len(m.body.rows); r++ {
		plain := ansi.Strip(m.body.rows[r])
		c0, c1 := cols(a, b, r, plain)
		line := ""
		if c0 < c1 {
			line = strings.TrimRight(ansi.Strip(ansi.Cut(plain, c0, c1)), " ")
		}
		lines = append(lines, line)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func (m *Model) paintSelection(row string, r int, p palette) string {
	a, b, ok := m.selRange()
	if !ok || r < a.row || r > b.row {
		return row
	}
	plain := ansi.Strip(row)
	c0, c1 := cols(a, b, r, plain)
	if c0 >= c1 {
		return row
	}
	w := ansi.StringWidth(plain)
	return ansi.Cut(row, 0, c0) + style(p.accentFg, p.accent).Render(ansi.Strip(ansi.Cut(row, c0, c1))) + ansi.Cut(row, c1, w)
}
