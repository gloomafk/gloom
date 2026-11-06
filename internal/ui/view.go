package ui

import (
	"fmt"
	"hash/maphash"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"gloom.im/gloom/internal/irc"
	"gloom.im/gloom/internal/version"
)

type palette struct {
	// edge is quieter than faint to distinguish pane borders from other rules.
	edge                                                    string
	bg, panel, bar, barFg, faint, fg, dim, accent, accentFg string
	hl, hlBg, act2, op, voice                               string
	nicks                                                   [6]string
}

const (
	gutterWidth = 12
	nicklistW   = 32
)

// Pseudo window indexes for the icon buttons in the status bar.
const (
	hitChannels = -1 - iota
	hitLayout
	hitConfig
	hitDebug
	hitLogo
)

var (
	darkPalette = palette{
		bg: "#0C0E13", panel: "#12151C", bar: "#1A1F2A", barFg: "#C9CED8", faint: "#262C3A", edge: "#1E2330",
		fg: "#E9EBF0", dim: "#7E8698", accent: "#5E9BFF", accentFg: "#0C0E13",
		hl: "#FFCB6B", hlBg: "#1F1D1A", act2: "#FF7A8A", op: "#FFCB6B", voice: "#80D99C",
		nicks: [6]string{"#8DB6FF", "#80D99C", "#F2B279", "#CFA4F2", "#72D7E0", "#F59AA6"},
	}
	lightPalette = palette{
		bg: "#FFFFFF", panel: "#F3F5F9", bar: "#E4E9F1", barFg: "#2A303B", faint: "#D5DCE6", edge: "#E0E5EC",
		fg: "#12151B", dim: "#5F6877", accent: "#1E5CCC", accentFg: "#FFFFFF",
		hl: "#8A5D00", hlBg: "#FFF6E1", act2: "#C0213D", op: "#8A5D00", voice: "#1E7A3A",
		nicks: [6]string{"#1E5CCC", "#1E7A3A", "#9A4A00", "#7A3DB0", "#0B6B78", "#B3243A"},
	}
)

func (m *Model) colors() palette {
	if m.light {
		return lightPalette
	}
	return darkPalette
}

type styleKey struct{ fg, bg string }

// Cache styles to avoid repeated hex parsing. Lip Gloss setters return copies;
// sync.Map allows parallel tests to share the cache safely.
var styles sync.Map // styleKey to lipgloss.Style

func style(fg, bg string) lipgloss.Style {
	k := styleKey{fg, bg}
	if s, ok := styles.Load(k); ok {
		return s.(lipgloss.Style)
	}
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg))
	styles.Store(k, s)
	return s
}

// fit truncates or pads s to width cells. Padding takes the given background.
func fit(s string, width int, bg string) string {
	s = ansi.Truncate(s, max(0, width), "")
	if gap := width - ansi.StringWidth(s); gap > 0 {
		s += style(bg, bg).Render(strings.Repeat(" ", gap))
	}
	return s
}
func (p palette) nickColor(n string) string {
	sum := 0
	for _, r := range n {
		sum += int(r)
	}
	return p.nicks[sum%len(p.nicks)]
}
func (p palette) prefixColor(pre string) string {
	switch pre {
	case "@", "~", "&", "%":
		return p.op
	case "+":
		return p.voice
	}
	return p.dim
}
func splitPrefix(n string) (string, string) {
	name := strings.TrimLeft(n, modePrefixes)
	return n[:len(n)-len(name)], name
}

func (m *Model) windowStyle(i int, p palette) (fg string, bold bool) {
	w := m.windows[i]
	switch {
	case m.showing(i):
		return p.accent, true
	case w.mention:
		return p.act2, true
	case w.unread > 0:
		return p.fg, false
	}
	return p.dim, false
}

// Three rows provide a shared middle row for text and images. Two rows
// misalign them. TestStatusBarTextAlignment guards this deliberate size.
const (
	statusRows    = 3
	statusTextRow = statusRows / 2
	// The divider is outside the three-row bar; bottomRows excludes the input row.
	dividerRows = 1
	bottomRows  = dividerRows + statusRows
	// Match the blank divider below the input; terminal padding uses whole rows.
	inputPadRows = 1
)

// cell is one status bar segment, one string per row, all the same width.
type cell [statusRows]string

func (c cell) width() int { return ansi.StringWidth(c[0]) }

// block builds a segment of the given width: mid on the text row, bg elsewhere.
// mid may carry its own styling.
func block(bg, mid string, width int) cell {
	var c cell
	for i := range c {
		row := ""
		if i == statusTextRow {
			row = mid
		}
		c[i] = fit(row, width, bg)
	}
	return c
}

func (m *Model) countStyle(i int, p palette, bg string) lipgloss.Style {
	if m.windows[i].mention && !m.showing(i) {
		return style(p.act2, bg).Bold(true)
	}
	return style(p.dim, bg)
}

type iconSpec struct {
	name, glyph, fg string
	index, flags    int
	on              bool
}

// Open views tint and underline their icons; layout always uses the accent.
func (m *Model) icons(p palette) []iconSpec {
	flags := 0
	if m.channels {
		flags |= layoutChannels
	}
	if m.nicklist {
		flags |= layoutNicks
	}
	if m.statusWindows() {
		flags |= layoutStatus
	}
	debug := m.showing(m.rawWindow())
	spec := func(name, glyph string, index int, on bool) iconSpec {
		ic := plainIcon(p, name, on)
		ic.glyph, ic.index = glyph, index
		return ic
	}
	return []iconSpec{
		spec("channels", "#", hitChannels, m.view == viewList),
		spec("config", "≡", hitConfig, m.view == viewConfig),
		spec("debug", "✱", hitDebug, debug),
		{"layout", "◫", p.accent, hitLayout, flags, false},
	}
}

func plainIcon(p palette, name string, on bool) iconSpec {
	fg := p.barFg
	if on {
		fg = p.accent
	}
	return iconSpec{name: name, fg: fg, on: on}
}

// iconVariants is every look an icon can take, for sending its images ahead of use.
func (m *Model) iconVariants(p palette) []iconSpec {
	var out []iconSpec
	for _, name := range []string{"channels", "config", "debug"} {
		out = append(out, plainIcon(p, name, false), plainIcon(p, name, true))
	}
	for flags := range 1 << 3 {
		out = append(out, iconSpec{name: "layout", fg: p.accent, flags: flags})
	}
	return out
}
func (m *Model) status() ([]string, []hit) {
	p := m.colors()
	// The prototype's 1px rules are an eighth of a cell, so they are "▕" drawn
	// at the right edge of a cell: the tab's last padding cell, and the cell
	// just before the button group. Neither adds width beyond the padding.
	rule := cell{}
	for i := range rule {
		rule[i] = style(p.faint, p.bar).Render("▕")
	}
	icons := m.icons(p)
	right := []cell{rule}
	var iconHits []hit
	at := m.width - len(icons)*iconCols
	for _, ic := range icons {
		if id, ok := m.placeholderID(iconKey(ic)); ok {
			var c cell
			for r := range c {
				c[r] = placeholderRow(id, r, iconCols, p.bar)
			}
			right = append(right, c)
		} else {
			mid := style(ic.fg, p.bar).Underline(ic.on).Render(" " + ic.glyph + " ")
			if m.logoImage() {
				mid = style(ic.fg, p.bar).Render(strings.Repeat(" ", iconCols))
			}
			right = append(right, block(p.bar, mid, iconCols))
		}
		iconHits = append(iconHits, hit{at, at + iconCols, ic.index})
		at += iconCols
	}
	rightW := 1 + len(icons)*iconCols

	var head cell
	// Without images the title is the word alone. Where images are wanted, the
	// mark's slot stays blank until the image is drawn, so the bar keeps its
	// shape when the images arrive or are redrawn after a resize.
	slot, title := "", " GLOOM "
	if m.imagesWanted() {
		slot, title = strings.Repeat(" ", logoCols), "GLOOM "
	}
	logoID, logoOK := m.placeholderID(logoKey(m.light))
	for i := range head {
		text := strings.Repeat(" ", len(title))
		if i == statusTextRow {
			text = title
		}
		if logoOK {
			head[i] = placeholderRow(logoID, i, logoCols, p.accent) + style(p.accentFg, p.accent).Bold(true).Render(text)
		} else {
			head[i] = style(p.accentFg, p.accent).Bold(true).Render(slot + text)
		}
	}
	left := []cell{head}
	x := head.width()
	hits := []hit{{0, head.width(), hitLogo}}
	avail := max(0, m.width-rightW)
	// Stop at the last page rather than scrolling the final tab into an
	// otherwise empty strip. A tab wider than the strip still gets a page.
	lastOff := max(1, len(m.windows)-1)
	used := 0
	if lastOff < len(m.windows) {
		used = m.statusTab(lastOff, p).width()
	}
	for lastOff > 1 {
		previous := m.statusTab(lastOff-1, p).width()
		if used+previous > avail-head.width() {
			break
		}
		used += previous
		lastOff--
	}
	m.tabOff = max(1, min(m.tabOff, lastOff))
	if m.revealWindows && m.active > 0 {
		m.tabOff = min(m.tabOff, m.active)
		used := 0
		for i := m.tabOff; i <= m.active; i++ {
			used += m.statusTab(i, p).width()
		}
		for m.tabOff < m.active && used > avail-head.width() {
			used -= m.statusTab(m.tabOff, p).width()
			m.tabOff++
		}
	}
	for i := m.tabOff; i < len(m.windows) && x < avail; i++ {
		if !m.statusWindows() {
			break
		}
		c := m.statusTab(i, p)
		left = append(left, c)
		hits = append(hits, hit{x, min(x+c.width(), avail), i})
		x += c.width()
	}
	out := make([]string, statusRows)
	for r := range out {
		var l strings.Builder
		for _, c := range left {
			l.WriteString(c[r])
		}
		out[r] = fit(l.String(), avail, p.bar)
		for _, c := range right {
			out[r] += c[r]
		}
	}
	return out, append(hits, iconHits...)
}

// statusTab builds one window tab, shared by rendering and visibility checks.
func (m *Model) statusTab(i int, p palette) cell {
	w := m.windows[i]
	fg, bold := m.windowStyle(i, p)
	bg := p.bar
	if m.showing(i) {
		bg = p.bg
	}
	mid := style(fg, bg).Bold(bold).Render(" " + w.name)
	if w.unread > 0 {
		mid += m.countStyle(i, p, bg).Render(fmt.Sprintf(" %d", w.unread))
	}
	mid += style(p.faint, bg).Render("▕")
	tabW := ansi.StringWidth(mid)
	c := block(bg, mid, tabW)
	for r := range c {
		if r != statusTextRow {
			c[r] = style(p.faint, bg).Render(strings.Repeat(" ", tabW-1) + "▕")
		}
	}
	return c
}

// eventLine describes how a nickless line is drawn, inferred from its text.
// Anything not recognised came from the server, so it gets the debug window's
// received arrow and colour.
func eventLine(p palette, text string) (glyph, color, body string) {
	switch {
	case strings.HasSuffix(text, " joined"):
		return "→", p.voice, text
	case strings.HasSuffix(text, " left"):
		return "←", p.act2, text
	case strings.Contains(text, " left: "):
		return "←", p.act2, strings.Replace(text, " left: ", " left · ", 1)
	case strings.Contains(text, " quit: "):
		return "←", p.act2, strings.Replace(text, " quit: ", " quit · ", 1)
	case strings.Contains(text, " is now "):
		return "⇄", p.accent, strings.Replace(text, " is now ", " → ", 1)
	}
	return "←", p.voice, text
}

// Keep only the last chat's formatted rows. Clock and cursor ticks should not
// rewrap and restyle the entire scrollback when its content has not changed.
type messageCacheKey struct {
	win        *window
	version    uint64
	width      int
	lines      int
	colors     palette
	nick       string
	timestamps bool
}

type messageCache struct {
	key  messageCacheKey
	rows []string
	hits []nickHit
}

func (m *Model) cachedMessageRows(w *window, p palette, width int) ([]string, []nickHit) {
	key := messageCacheKey{w, m.rowsVersion, width, len(w.lines), p, m.nick, m.timestamps}
	if m.chatRows.key != key {
		rows, hits := m.messageRows(w, p, width)
		m.chatRows = messageCache{key, rows, hits}
	}
	return m.chatRows.rows, m.chatRows.hits
}

func (m *Model) messageRows(w *window, p palette, mainW int) ([]string, []nickHit) {
	prefixW := 1 + gutterWidth + 3
	if m.timestamps {
		prefixW += timeW
	}
	textW := max(1, mainW-prefixW-1)
	self := irc.Fold(m.nick)
	var rows []string
	var hits []nickHit
	for _, l := range w.lines {
		own := l.nick != "" && irc.Fold(l.nick) == self
		hl := l.nick != "" && !own && mentions(l.text, self)
		bg, fg := p.bg, p.fg
		if hl {
			bg = p.hlBg
		}
		textStyle := style(fg, bg)
		var gutter, rule string
		var nick string
		body := l.text
		switch {
		case l.nick != "":
			pre, name := splitPrefix(l.nick)
			if n, ok := w.nicks[irc.Fold(l.nick)]; ok {
				pre, name = splitPrefix(n)
			}
			nick = name
			color := p.nickColor(name)
			nickStyle := style(color, bg)
			switch {
			case own:
				color = p.accent
				nickStyle = style(color, bg).Bold(true)
			case hl:
				color = p.hl
				nickStyle = style(color, bg).Bold(true)
			}
			shown := ansi.Truncate(name, max(1, gutterWidth-len(pre)), "…")
			gutter = style(p.dim, bg).Render(pre) + nickStyle.Render(shown)
			rule = color
		case strings.HasPrefix(l.text, "* "):
			actor, _, _ := strings.Cut(strings.TrimPrefix(l.text, "* "), " ")
			color := p.nickColor(actor)
			gutter = style(color, bg).Render("•")
			rule = color
			body = strings.TrimPrefix(l.text, "* ")
			textStyle = textStyle.Italic(true)
		default:
			var color string
			var glyph string
			glyph, color, body = eventLine(p, l.text)
			switch {
			case l.code != "":
				glyph = l.code
			case l.client:
				glyph, color = "→", p.accent
			}
			gutter = style(color, bg).Render(glyph)
			rule = p.faint
			textStyle = style(p.dim, bg)
		}
		head := style(p.fg, bg).Render(" ")
		if m.timestamps {
			head += style(p.dim, bg).Render(l.at.Format("15:04") + "  ")
		}
		gap := max(0, gutterWidth-ansi.StringWidth(gutter))
		head += style(p.fg, bg).Render(strings.Repeat(" ", gap)) + gutter + style(rule, bg).Render(" ▏ ")
		blank := style(p.fg, bg).Render(strings.Repeat(" ", prefixW))
		if nick != "" {
			start := 1 + gap
			if m.timestamps {
				start += timeW
			}
			hits = append(hits, nickHit{start: start, end: min(start+ansi.StringWidth(gutter), mainW), row: len(rows), nick: nick})
		}
		rows = appendWrapped(rows, ansi.Wrap(body, textW, " "), head, blank, textStyle, mainW, bg)
	}
	return rows, hits
}

var nickSeed = maphash.MakeSeed()

// Summing makes the signature independent of map iteration order,
// avoiding a sort and restyle when the nick map has not changed.
func nickSig(nicks map[string]string) uint64 {
	sig := uint64(len(nicks))
	for k, v := range nicks {
		sig += maphash.String(nickSeed, k) ^ maphash.String(nickSeed, v)*3
	}
	return sig
}

type nickCache struct {
	win   *window
	sig   uint64
	names []string
	// rows holds styled rows by prefixed nick, filled as they are drawn, so a
	// scroll restyles only rows not seen since the last membership change.
	key  nickRowKey
	rows map[string]string
}

// nickRowKey is everything a styled nicklist row depends on besides its nick.
type nickRowKey struct {
	p    palette
	cols int
	self string
}

// sortedNicks returns w's nicks, highest rank first and then by folded name.
func (m *Model) sortedNicks(w *window) []string {
	sig := nickSig(w.nicks)
	if m.nickList.win == w && m.nickList.sig == sig {
		return m.nickList.names
	}
	names := slices.Collect(maps.Values(w.nicks))
	rank := func(n string) int {
		pre, _ := splitPrefix(n)
		if pre == "" {
			return len(modePrefixes)
		}
		return strings.IndexByte(modePrefixes, pre[0])
	}
	slices.SortFunc(names, func(a, b string) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return strings.Compare(irc.Fold(a), irc.Fold(b))
	})
	m.nickList = nickCache{win: w, sig: sig, names: names}
	return names
}

func (m *Model) nickRows(w *window, p palette, height int) ([]string, []nickHit) {
	nw := m.nlCols
	cw := nw - 1
	names := m.sortedNicks(w)
	count := fmt.Sprint(len(names))
	label := style(p.dim, p.bg).Bold(true).Render(" NICKS")
	gap := max(1, cw-ansi.StringWidth(label)-len(count)-1)
	head := label + style(p.dim, p.bg).Render(strings.Repeat(" ", gap)+count+" ")
	rows := []string{fit(head, cw, p.bg)}
	var hits []nickHit
	self := irc.Fold(m.nick)
	visible := max(0, height-1)
	w.nickOff = max(0, min(w.nickOff, len(names)-visible))
	end := min(len(names), w.nickOff+visible)
	key := nickRowKey{p, cw, self}
	if m.nickList.rows == nil || m.nickList.key != key {
		m.nickList.key, m.nickList.rows = key, make(map[string]string, visible)
	}
	for _, n := range names[w.nickOff:end] {
		_, name := splitPrefix(n)
		row, ok := m.nickList.rows[n]
		if !ok {
			row = nickRow(n, p, cw, self)
			m.nickList.rows[n] = row
		}
		if len(rows) < height {
			// The whole row is the target, past the end of the name. Column 0
			// is left out: a press there grabs the pane border.
			hits = append(hits, nickHit{start: 1, end: nw, row: len(rows), nick: name})
		}
		rows = append(rows, row)
	}
	for len(rows) < height {
		rows = append(rows, fit("", cw, p.bg))
	}
	rows = rows[:height]
	edgeFg := p.edge
	if m.drag == paneNicklist {
		edgeFg = p.accent
		rows[height-1] = fit(dragBadge(p, nw), cw, p.bg)
	}
	edge := style(edgeFg, p.bg).Render("│")
	for i := range rows {
		rows[i] = edge + rows[i]
	}
	return rows, hits
}

// nickRow styles one member, with its highest mode prefix, to cols cells.
// self is the user's own folded nick, which is highlighted.
func nickRow(n string, p palette, cols int, self string) string {
	pre, name := splitPrefix(n)
	nameStyle := style(p.fg, p.bg)
	if irc.Fold(name) == self {
		nameStyle = style(p.accent, p.bg).Bold(true)
	}
	if pre == "" {
		pre = " "
	}
	pre = pre[:1]
	row := style(p.fg, p.bg).Render(" ") + style(p.prefixColor(pre), p.bg).Render(pre) + style(p.fg, p.bg).Render(" ") + nameStyle.Render(name)
	return fit(row, cols, p.bg)
}

func (m *Model) topicBar(p palette, w *window, width int) string {
	name, topic := w.name, w.topic
	switch {
	case m.view == viewList:
		name, topic = "channels", "/list on "+m.network
	case m.view == viewConfig:
		name, topic = "config", "read-only · v"+version.Number+" keeps no config file"
	case m.view == viewSplash:
		name, topic = "gloom", "v"+version.Number+" · /splash"
	case w.raw:
		topic = m.network + " · raw traffic · → sent  ← received"
	case m.active == 0:
		topic = "v" + version.Number + " · /help"
	case w.server:
		topic = "not connected"
		if m.registered && w == m.srv {
			topic = m.network + " · connected as " + m.nick
		}
	}
	// The nicklist title already shows the member count. The topic bar does not repeat it.
	s := style(p.accent, p.panel).Bold(true).Render(" "+name) + style(p.fg, p.panel).Render("  "+topic)
	return fit(s, width, p.panel)
}

// Keep the divider blank, matching inputPadRows above the input.
func (m *Model) divider(p palette, width int) string {
	return fit("", width, p.bg)
}

// cursorBlink is the prototype's cursor period: half a second shown, half hidden.
const cursorBlink = 500 * time.Millisecond

func (m *Model) inputRow(p palette, width int) string {
	text := style(p.fg, p.bg)
	switch {
	case m.view == viewList:
		m.input.Prompt = "filter ❯ "
	case m.showing(m.active) && m.current().raw:
		m.input.Prompt = "/ "
	default:
		m.input.Prompt = "❯ "
	}
	m.input.SetWidth(max(1, width-2-ansi.StringWidth(m.input.Prompt)))
	// SetStyles resets the cursor to shown; restyling every frame cancels blinking.
	if p != m.inputPalette {
		blurred := textinput.StyleState{Text: text, Placeholder: style(p.dim, p.bg), Suggestion: style(p.dim, p.bg), Prompt: style(p.accent, p.bg)}
		focused := blurred
		focused.Prompt = focused.Prompt.Bold(true)
		m.input.SetStyles(textinput.Styles{
			Focused: focused,
			Blurred: blurred,
			Cursor:  textinput.CursorStyle{Color: lipgloss.Color(p.accent), Shape: tea.CursorBlock, Blink: true, BlinkSpeed: cursorBlink},
		})
		m.inputPalette = p
	}
	return fit(text.Render(" ")+m.input.View(), width, p.bg)
}

// minWidth and minHeight are the smallest terminal the layout is drawn in.
// Below them View shows only a request to enlarge it, and no images.
const (
	minWidth  = 30
	minHeight = 8
)

func (m *Model) fitsLayout() bool { return m.width >= minWidth && m.height >= minHeight }

// adjustPendingRows keeps scrolled-back content in place as wrapped lines arrive.
func (m *Model) adjustPendingRows(w *window, p palette, width int) {
	if m.view != viewWindow || w.pending <= 0 {
		return
	}
	pending := w.pending
	w.pending = 0
	if w.offset <= 0 {
		return
	}
	// Offsets are in wrapped rows, while arrivals are protocol lines.
	// Include continuation rows to hold the viewport in place.
	if w.raw {
		count := min(pending, len(m.raw))
		added := rawRowsFor(m.raw[len(m.raw)-count:], p, width)
		w.offset += len(added) - count
		return
	}
	count := min(pending, len(w.lines))
	tail := *w
	tail.lines = w.lines[len(w.lines)-count:]
	added, _ := m.messageRows(&tail, p, width)
	w.offset += len(added) - count
}

// View renders the current screen. It records only the layout that mouse hit
// testing needs and never touches the connection.
func (m *Model) View() tea.View {
	m.nickHits = nil
	m.nickWindow = m.active
	p := m.colors()
	w := m.current()
	width := max(1, m.width)
	height := max(1, m.height)
	if !m.fitsLayout() {
		m.body = bodyGeometry{}
		blank := fit("", width, p.bg)
		lines := make([]string, height)
		for i := range lines {
			lines[i] = blank
		}
		lines[0] = fit(style(p.fg, p.bg).Render("gloom: enlarge terminal"), width, p.bg)
		v := tea.NewView(strings.Join(lines, "\n"))
		v.AltScreen = true
		return v
	}
	bodyH := height - 2 - inputPadRows - bottomRows
	left, side := 0, 0
	if m.channels && width-m.clCols >= 30 {
		left = m.clCols
	}
	chat := m.view == viewWindow && !w.raw
	if chat && m.nicklist && isChannel(w.name) && width-left-m.nlCols >= 45 {
		side = m.nlCols
	}
	m.paneL, m.paneR = left, side
	mainW := width - left - side
	var rows []string
	var messageHits []nickHit
	bottom := true // chat views stick to the bottom, panels start at the top
	m.listIdx = nil
	switch {
	case m.view == viewList:
		rows, m.listIdx = m.listRows(p, mainW)
		bottom = false
	case m.view == viewConfig:
		rows, bottom = m.configRows(p, mainW), false
	case m.view == viewSplash:
		rows = m.splashRows(p, mainW, bodyH)
	case w.raw:
		rows = m.rawRows(p, mainW)
	default:
		rows, messageHits = m.cachedMessageRows(w, p, mainW)
	}
	m.adjustPendingRows(w, p, mainW)
	start, end := 0, 0
	if bottom {
		w.offset = min(w.offset, max(0, len(rows)-bodyH))
		end = max(0, len(rows)-w.offset)
		start = max(0, end-bodyH)
	} else {
		m.panelOff = min(m.panelOff, max(0, len(rows)-bodyH))
		start = m.panelOff
		end = min(len(rows), start+bodyH)
	}
	m.body = bodyGeometry{x: left, w: mainW, h: bodyH, start: start, rows: rows}
	if m.view != viewWindow {
		m.body.rows = nil
	}
	if m.sel.win != nil && m.sel.sig != m.selSig() {
		m.sel = selection{}
	}
	visible := slices.Clone(rows[start:end])
	for i := range visible {
		visible[i] = m.paintSelection(visible[i], start+i, p)
	}
	for _, h := range messageHits {
		if h.row < start || h.row >= end {
			continue
		}
		h.row = h.row - start + 1
		h.start += left
		h.end += left
		m.nickHits = append(m.nickHits, h)
	}
	if m.view == viewList {
		m.listIdx = m.listIdx[start:end]
	}
	for len(visible) < bodyH {
		visible = append(visible, fit("", mainW, p.bg))
	}
	// Side panes span the full column, including topic and input.
	// Pane row indexes therefore match screen row indexes.
	tail := []string{fit("", mainW, p.bg), m.inputRow(p, mainW), m.divider(p, mainW)}
	column := slices.Concat([]string{m.topicBar(p, w, mainW)}, visible, tail)
	if left > 0 {
		pane := m.chanlistRows(p, len(column))
		for i := range column {
			column[i] = pane[i] + column[i]
		}
	}
	if side > 0 {
		nicks, hits := m.nickRows(w, p, len(column))
		for _, h := range hits {
			h.start += left + mainW
			h.end += left + mainW
			m.nickHits = append(m.nickHits, h)
		}
		for i := range column {
			column[i] += nicks[i]
		}
	}
	status, _ := m.status()
	lines := slices.Concat(column, status)
	if m.layMenu {
		overlay(lines, m.layoutMenu(p), width, height-bottomRows)
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	m.revealWindows = false
	return view
}
