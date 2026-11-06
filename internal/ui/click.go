package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// queryable reports whether a listed nick can name a private window. Servers
// accept more than ValidNick does (leading digits, UTF-8), so only what would
// misroute or break a PRIVMSG target is refused. Send still rejects bad lines.
func queryable(nick string) bool {
	return nick != "" && len(nick) <= 64 && !strings.ContainsAny(nick, " ,:") && !strings.ContainsRune("#&", rune(nick[0]))
}

// nickAt returns the nick drawn at a screen cell of the active window, if any.
func (m *Model) nickAt(x, y int) *nickHit {
	if m.nickWindow != m.active {
		return nil
	}
	for _, h := range m.nickHits {
		if y == h.row && x >= h.start && x < h.end && queryable(h.nick) {
			return &h
		}
	}
	return nil
}

// click handles a mouse press. Left clicks act. A right click on the layout
// button opens its menu.
func (m *Model) click(v tea.MouseClickMsg) tea.Cmd {
	menu := m.layMenu
	m.layMenu = false
	if m.view == viewSplash {
		m.view = viewWindow
		return nil
	}
	if v.Button == tea.MouseLeft && m.grab(v.X, v.Y) {
		return nil
	}
	var hits []hit
	if v.Y >= m.height-statusRows {
		_, hits = m.status()
	}
	if v.Button == tea.MouseRight {
		for _, h := range hits {
			if h.index == hitLayout && v.X >= h.start && v.X < h.end {
				m.layMenu = !menu
			}
		}
		return nil
	}
	if v.Button != tea.MouseLeft {
		return nil
	}
	if menu {
		return nil
	}
	// A press in the message area may start a selection, so a nick there waits
	// for the release. The nicklist is outside it and acts at once.
	if m.beginSelect(v.X, v.Y) {
		return nil
	}
	if h := m.nickAt(v.X, v.Y); h != nil && m.view == viewWindow {
		m.selectWindow(m.ensure(h.nick))
		m.nickHits = nil
		return nil
	}
	if m.view == viewList && v.X >= m.paneL && v.Y >= 1 && v.Y-1 < len(m.listIdx) && m.listIdx[v.Y-1] >= 0 {
		m.joinChannel(m.filteredList()[m.listIdx[v.Y-1]].name)
		return nil
	}
	// The title stays at row 0; the remaining rows follow the pane's offset.
	if m.paneL > 0 && v.X < m.paneL-1 && v.Y >= 1 && m.windowOff+v.Y-1 < len(m.windows) && v.Y < m.height-statusRows {
		m.selectWindow(m.windowOff + v.Y - 1)
		return nil
	}
	for _, h := range hits {
		if v.X < h.start || v.X >= h.end {
			continue
		}
		switch h.index {
		case hitChannels:
			m.toggleList()
		case hitConfig:
			m.toggleView(viewConfig)
		case hitDebug:
			m.toggleDebug()
		case hitLayout:
			m.cycleLayout()
		case hitLogo:
			m.selectWindow(0)
		default:
			m.selectWindow(h.index)
		}
	}
	return nil
}
