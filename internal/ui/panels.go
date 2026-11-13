package ui

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"gloom.im/gloom/internal/irc"
	"gloom.im/gloom/internal/version"
)

// viewMode is what fills the main area. The window views (chat, debug) belong
// to m.active. The others are overlays and leave m.active alone.
type viewMode int

const (
	viewWindow viewMode = iota
	viewList
	viewConfig
	viewSplash
)

const (
	chanlistW = 32
	chanW     = 18 // width of the channel column in the channel list
	maxRaw    = 400
	maxList   = 5000
	menuW     = 30
	timeW     = 7 // a "15:04" timestamp and its two spaces
)

const (
	rawSent     byte = '>'
	rawReceived byte = '<'
	rawNote     byte = '!'
)

type listEntry struct {
	name  string
	users int
	topic string
}
type rawLine struct {
	at   time.Time
	kind byte // rawSent, rawReceived or rawNote
	text string
}

var layouts = [][3]bool{
	{true, false, true}, {true, true, false}, {false, true, false},
	{false, false, true}, {false, false, false},
}

func (m *Model) statusWindows() bool { return m.sb && !m.channels }
func (m *Model) layoutIndex() int {
	return slices.Index(layouts, [3]bool{m.nicklist, m.channels, m.statusWindows()})
}
func (m *Model) layoutName() string {
	var parts []string
	for _, p := range []struct {
		on   bool
		name string
	}{{m.nicklist, "nicks"}, {m.channels, "channels"}, {m.statusWindows(), "status"}} {
		if p.on {
			parts = append(parts, p.name)
		}
	}
	if len(parts) == 0 {
		return "bare"
	}
	return strings.Join(parts, " + ")
}
func (m *Model) cycleLayout() {
	next := layouts[(m.layoutIndex()+1)%len(layouts)]
	m.nicklist, m.channels, m.sb = next[0], next[1], next[2]
	m.layMenu = false
}

func (m *Model) toggleView(v viewMode) {
	m.layMenu = false
	m.input.SetValue("")
	if m.view == v {
		m.view = viewWindow
		return
	}
	m.view = v
	m.panelOff = 0
}
func (m *Model) toggleList() {
	m.toggleView(viewList)
	if m.view == viewList && m.client != nil && !m.listing && len(m.list) == 0 {
		m.listing = m.send("LIST")
	}
}
func (m *Model) rawWindow() int {
	return slices.IndexFunc(m.windows, func(w *window) bool { return w.raw })
}

func (m *Model) toggleDebug() {
	m.layMenu = false
	i := m.rawWindow()
	if i >= 0 && m.showing(i) {
		prev := min(m.prevWin, len(m.windows)-2)
		m.closeWindow(i)
		m.selectWindow(max(prev, 0))
		return
	}
	if i < 0 {
		debug := newWindow("debug")
		debug.raw = true
		m.windows = append(m.windows, debug)
		i = len(m.windows) - 1
	}
	if m.view != viewWindow || m.active != i {
		m.prevWin = m.active
	}
	m.selectWindow(i)
}
func (m *Model) logRaw(kind byte, text string) {
	m.raw = append(m.raw, rawLine{time.Now(), kind, irc.Clean(text)})
	if i := m.rawWindow(); i >= 0 && m.windows[i].offset > 0 {
		m.windows[i].offset++
		m.windows[i].pending++
	}
	if len(m.raw) > maxRaw {
		dropped := len(m.raw) - maxRaw
		clear(m.raw[:dropped])
		m.raw = m.raw[dropped:]
		m.firstRaw += uint64(dropped)
		if m.sel.win != nil && m.sel.win.raw {
			m.sel = selection{}
		}
	}
}

func (m *Model) rawInput(s string) {
	if m.client == nil {
		m.logRaw(rawNote, "not connected")
		return
	}
	if err := m.client.Send(strings.TrimPrefix(s, "/")); err != nil {
		m.logRaw(rawNote, err.Error())
	}
}
func (m *Model) joinChannel(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if !isChannel(name) {
		name = "#" + name
	}
	if !irc.Atom(name) {
		m.note(m.active, "Invalid channel name.")
		return
	}
	if i := m.find(name); i >= 0 && m.windows[i].joined {
		m.selectWindow(i)
		return
	}
	if !m.send("JOIN " + name) {
		return
	}
	if m.pendingJoins == nil {
		m.pendingJoins = map[string]bool{}
	}
	m.pendingJoins[irc.Fold(name)] = true
}

// The channel list is sorted when LIST finishes, not on every frame. Each
// entry is folded once into m.listKeys, which follows m.list by index and is
// reset whenever entries are replaced or reordered.
func (m *Model) filteredList() []listEntry {
	q := irc.Fold(strings.TrimSpace(m.input.Value()))
	if q == "" {
		return m.list
	}
	if len(m.listKeys) > len(m.list) {
		m.listKeys = nil
	}
	for _, e := range m.list[len(m.listKeys):] {
		m.listKeys = append(m.listKeys, irc.Fold(e.name)+"\x00"+irc.Fold(e.topic))
	}
	var hits []listEntry
	for i, e := range m.list {
		if strings.Contains(m.listKeys[i], q) {
			hits = append(hits, e)
		}
	}
	return hits
}
func (m *Model) listSubmit(s string) {
	if hits := m.filteredList(); len(hits) > 0 {
		m.joinChannel(hits[0].name)
	} else {
		m.joinChannel(s)
	}
	if m.view == viewList && m.active != 0 {
		m.view = viewWindow
	}
}
func (m *Model) receiveList(code string, a []string) {
	switch code {
	case "321":
		m.list, m.listKeys, m.listing = nil, nil, true
	case "322":
		if len(a) < 3 || len(m.list) >= maxList {
			return
		}
		n, _ := strconv.Atoi(a[2])
		topic := ""
		if len(a) > 3 {
			topic = a[3]
		}
		m.list = append(m.list, listEntry{irc.Clean(a[1]), n, irc.Clean(topic)})
	case "323":
		m.listing, m.listKeys = false, nil
		slices.SortStableFunc(m.list, func(a, b listEntry) int { return b.users - a.users })
	}
}

func (m *Model) splashRows(p palette, mainW, bodyH int) []string {
	rows := make([]string, bodyH)
	for i := range rows {
		rows[i] = fit("", mainW, p.bg)
	}
	rows[bodyH-1] = fit(style(p.dim, p.bg).Render(" "+version.Name), mainW, p.bg)
	return rows
}

func padLeft(s string, width int) string {
	s = ansi.Truncate(s, width, "")
	return strings.Repeat(" ", max(0, width-ansi.StringWidth(s))) + s
}

// listRows returns one row per channel plus the header, but styles only the
// rows that are visible at m.panelOff within bodyH. The rest stay empty, so
// styling work per redraw does not grow with the number of channels.
func (m *Model) listRows(p palette, mainW, bodyH int) ([]string, []int) {
	hits := m.filteredList()
	count := fmt.Sprintf("%d channels", len(hits))
	if q := strings.TrimSpace(m.input.Value()); q != "" {
		count = fmt.Sprintf("%d of %d channels", len(hits), len(m.list))
	}
	if m.listing {
		count = "loading... " + count
	}
	dim, bg := style(p.dim, p.bg), p.bg
	head := dim.Render(" channel" + strings.Repeat(" ", chanW-len("channel")) + "  " + padLeft("users", 7) + "  topic")
	gap := mainW - ansi.StringWidth(head) - len(count) - 1
	if gap > 0 {
		head += dim.Render(strings.Repeat(" ", gap) + count + " ")
	}
	rows, idx := []string{fit(head, mainW, bg)}, []int{-1}
	if len(hits) == 0 {
		note := " no channels yet. /connect to a server first."
		switch {
		case m.listing:
			note = " waiting for the server..."
		case m.client != nil && len(m.list) > 0:
			note = fmt.Sprintf(" no channels match “%s”. Enter to join it.", strings.TrimSpace(m.input.Value()))
		case m.client != nil:
			note = " the server returned no channels."
		}
		return append(rows, fit(dim.Render(note), mainW, bg)), append(idx, -1)
	}
	total := len(hits) + 1
	rows, idx = slices.Grow(rows, len(hits)), slices.Grow(idx, len(hits))
	rows, idx = rows[:total], idx[:total]
	for i := range hits {
		idx[i+1] = i
	}
	m.panelOff = min(m.panelOff, max(0, total-bodyH))
	first := max(0, m.panelOff-1)
	last := min(len(hits), m.panelOff-1+bodyH)
	for i := first; i < last; i++ {
		e := hits[i]
		joined := false
		if wi := m.find(e.name); wi >= 0 {
			joined = m.windows[wi].joined
		}
		nameStyle, tag := style(p.fg, bg), ""
		if joined {
			nameStyle, tag = style(p.accent, bg).Bold(true), "joined"
		}
		name := ansi.Truncate(e.name, chanW, "")
		left := style(p.fg, bg).Render(" ") + nameStyle.Render(name) +
			style(p.fg, bg).Render(strings.Repeat(" ", chanW-ansi.StringWidth(name))+"  ") +
			dim.Render(padLeft(thousands(e.users), 7)) + style(p.fg, bg).Render("  ")
		room := mainW - ansi.StringWidth(left) - 1
		if tag != "" {
			room -= len(tag) + 1
		}
		row := left + style(p.fg, bg).Render(ansi.Truncate(e.topic, max(0, room), "…"))
		if tag != "" {
			row = fit(row, mainW-len(tag)-1, bg) + style(p.voice, bg).Render(tag) + style(p.fg, bg).Render(" ")
		}
		rows[i+1] = fit(row, mainW, bg)
	}
	return rows, idx
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// find returns the window index for name, or -1 without creating one.
func (m *Model) find(name string) int {
	folded := irc.Fold(name)
	return slices.IndexFunc(m.windows, func(w *window) bool { return !w.raw && irc.Fold(w.name) == folded })
}

var (
	reSection = regexp.MustCompile(`^(\s*)(\[[^\]]+\])(.*)$`)
	reKV      = regexp.MustCompile(`^(\s*)("[^"]*"|[\w.-]+)(\s*=\s*)(.*)$`)
	reValue   = regexp.MustCompile(`("[^"]*"|\btrue\b|\bfalse\b|\b\d+\b)`)
)

func (m *Model) configText() string {
	host, port, useTLS := m.conf.Host, m.conf.Port, m.conf.TLS
	if host == "" {
		host, useTLS = "irc.gloom.im", true
	}
	if port == "" {
		port = "6697"
	}
	// The USER line sends the nickname when no realname is set.
	realname := cmp.Or(m.realname, m.nick)
	palette := "dark"
	if m.light {
		palette = "light"
	}
	return fmt.Sprintf(`# %s
# read-only: settings last for this session, nothing is saved

[server]
host      = %q
port      = %s
tls       = %t

[identity]
nick      = %q
realname  = %q

[ui]
palette    = %q
timestamps = %t

[layout]
nicklist  = %t
chanlist  = %t
statusbar = %t
nicklist_width = %d
chanlist_width = %d
`, version.Name, host, port, useTLS, m.nick, realname, palette, m.timestamps, m.nicklist, m.channels, m.statusWindows(), m.nlCols, m.clCols)
}
func (m *Model) configRows(p palette, mainW int) []string {
	var rows []string
	for i, ln := range strings.Split(strings.TrimRight(m.configText(), "\n"), "\n") {
		body, comment := ln, ""
		if c := commentAt(ln); c >= 0 {
			body, comment = ln[:c], ln[c:]
		}
		s := configBody(p, body)
		if comment != "" {
			s += style(p.dim, p.bg).Italic(true).Render(comment)
		}
		num := style(p.faint, p.bg).Render(fmt.Sprintf("%4d  ", i+1))
		rows = append(rows, fit(style(p.fg, p.bg).Render(" ")+num+s, mainW, p.bg))
	}
	return rows
}

// configBody highlights a section, a key/value pair, or plain config text.
func configBody(p palette, body string) string {
	if sec := reSection.FindStringSubmatch(body); sec != nil {
		return style(p.fg, p.bg).Render(sec[1]) +
			style(p.accent, p.bg).Bold(true).Render(sec[2]) +
			style(p.fg, p.bg).Render(sec[3])
	}
	kv := reKV.FindStringSubmatch(body)
	if kv == nil {
		return style(p.fg, p.bg).Render(body)
	}
	var s strings.Builder
	put := func(fg, t string) { s.WriteString(style(fg, p.bg).Render(t)) }
	put(p.fg, kv[1]+kv[2])
	put(p.dim, kv[3])
	rest := kv[4]
	last := 0
	for _, loc := range reValue.FindAllStringIndex(rest, -1) {
		put(p.dim, rest[last:loc[0]])
		tok := rest[loc[0]:loc[1]]
		fg := p.hl
		if strings.HasPrefix(tok, "\"") {
			fg = p.voice
		}
		put(fg, tok)
		last = loc[1]
	}
	put(p.dim, rest[last:])
	return s.String()
}

// commentAt finds a # outside quotes.
func commentAt(s string) int {
	quoted := false
	for i, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '#' && !quoted:
			return i
		}
	}
	return -1
}

// rawCache keeps the bounded debug history formatted across arrivals and ticks.
type rawCache struct {
	width  int
	colors palette
	rowCache
}

func (m *Model) rawRows(p palette, mainW int) []string {
	c := &m.debugRows
	if c.width != mainW || c.colors != p {
		*c = rawCache{width: mainW, colors: p, first: m.firstRaw}
	}
	c.trim(m.firstRaw)
	appendRawRows(m.raw, p, mainW, &c.rowCache)
	return c.rows
}

func rawRowsFor(lines []rawLine, p palette, mainW int) []string {
	var c rowCache
	appendRawRows(lines, p, mainW, &c)
	return c.rows
}

func appendRawRows(lines []rawLine, p palette, mainW int, c *rowCache) {
	if len(c.ends) == len(lines) {
		return
	}
	prefixW := 1 + timeW + gutterWidth + 3
	textW := max(1, mainW-prefixW)
	blank := style(p.fg, p.bg).Render(strings.Repeat(" ", prefixW))
	rows := c.rows
	for _, l := range lines[len(c.ends):] {
		glyph, color, fg := "!", p.accent, p.dim
		switch l.kind {
		case rawSent:
			glyph, color, fg = "→", p.accent, p.accent
		case rawReceived:
			glyph, color, fg = "←", p.voice, p.fg
		}
		if strings.HasPrefix(l.text, "PING") || strings.HasPrefix(l.text, "PONG") {
			fg = p.dim
		}
		head := style(p.fg, p.bg).Render(" ") + style(p.dim, p.bg).Render(l.at.Format("15:04")+"  ") +
			style(p.fg, p.bg).Render(strings.Repeat(" ", gutterWidth-1)) + style(color, p.bg).Bold(true).Render(glyph) +
			style(p.faint, p.bg).Render(" ▏ ")
		rows = appendWrapped(rows, ansi.Hardwrap(l.text, textW, true), head, blank, style(fg, p.bg), mainW, p.bg)
		c.ends = append(c.ends, len(rows))
	}
	c.rows = rows
}

// appendWrapped adds the rows of already wrapped text: the first led by head
// and the others by blank, which is as wide.
func appendWrapped(rows []string, wrapped, head, blank string, text lipgloss.Style, width int, bg string) []string {
	lead := head
	for part := range strings.SplitSeq(wrapped, "\n") {
		rows = append(rows, fit(lead+text.Render(part), width, bg))
		lead = blank
	}
	return rows
}

func (m *Model) chanlistRows(p palette, height int) []string {
	w := m.paneL
	edgeFg := p.edge
	if m.drag == paneChanlist {
		edgeFg = p.accent
	}
	edge := style(edgeFg, p.bg).Render("│")
	pane := func(s string) string { return fit(s, w-1, p.bg) + edge }
	rows := []string{pane(style(p.dim, p.bg).Bold(true).Render(" WINDOWS"))}
	count := max(0, height-1)
	m.windowOff = max(0, min(m.windowOff, len(m.windows)-count))
	if m.revealWindows {
		m.windowOff = max(0, min(m.windowOff, m.active))
		if m.active >= m.windowOff+count {
			m.windowOff = max(0, m.active-count+1)
		}
	}
	end := min(len(m.windows), m.windowOff+count)
	for i := m.windowOff; i < end; i++ {
		win := m.windows[i]
		fg, bold := m.windowStyle(i, p)
		bg := p.bg
		numFg := p.dim
		if m.showing(i) {
			bg, numFg = p.panel, p.accent
		}
		count := ""
		if win.unread > 0 {
			count = strconv.Itoa(win.unread)
		}
		name := ansi.Truncate(win.name, max(1, w-1-4-len(count)-1), "…")
		row := style(numFg, bg).Render(fmt.Sprintf(" %d ", i+1)) + style(fg, bg).Bold(bold).Render(" "+name)
		gap := max(0, w-1-ansi.StringWidth(row)-len(count)-1)
		row += style(fg, bg).Render(strings.Repeat(" ", gap)) + m.countStyle(i, p, bg).Render(count+" ")
		rows = append(rows, fit(row, w-1, bg)+edge)
	}
	for len(rows) < height {
		rows = append(rows, pane(""))
	}
	if m.drag == paneChanlist && height > 1 {
		badge := dragBadge(p, w)
		rows[height-1] = pane(fit(rows[height-1], w-1-ansi.StringWidth(badge)-1, p.bg) + badge)
	}
	return rows[:height]
}

func (m *Model) layoutMenu(p palette) []string {
	in := menuW - 2
	frame := style(p.faint, p.panel)
	line := func(s string) string { return frame.Render("│") + fit(s, in, p.panel) + frame.Render("│") }
	two := func(left, right string, fg string) string {
		gap := max(1, in-2-ansi.StringWidth(left)-ansi.StringWidth(right))
		return style(fg, p.panel).Render(" " + left + strings.Repeat(" ", gap) + right + " ")
	}
	horizontal := strings.Repeat("─", in)
	return []string{
		frame.Render("┌" + horizontal + "┐"),
		line(two("layout", fmt.Sprintf("%d/%d", m.layoutIndex()+1, len(layouts)), p.dim)),
		line(style(p.fg, p.panel).Render(" " + m.layoutName())),
		line(frame.Render(horizontal)),
		line(two("Save as default", "no config", p.dim)),
		line(style(p.dim, p.panel).Render(" default: nicks + status")),
		frame.Render("└" + horizontal + "┘"),
	}
}

// overlay draws box over lines, right aligned, ending on row bottom.
func overlay(lines, box []string, width, bottom int) {
	x := width - menuW
	for i, b := range box {
		y := bottom - len(box) + 1 + i
		if y < 0 || y >= len(lines) {
			continue
		}
		lines[y] = fit(lines[y], x, "#000000") + b
	}
}

// closeOverlays leaves list, config, splash and the layout menu. It reports
// whether one was open.
func (m *Model) closeOverlays() bool {
	was := m.view != viewWindow || m.layMenu
	m.view, m.layMenu = viewWindow, false
	return was
}
