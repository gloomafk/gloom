// Package ui implements the v0.0.1 terminal interface.
package ui

import (
	"context"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"gloom.im/gloom/internal/irc"
	"gloom.im/gloom/internal/version"
)

const (
	maxLines   = 2000 // scrollback kept per window
	maxHistory = 200  // input lines remembered

	connectTimeout = 15 * time.Second // the whole connection attempt, TLS included
	pingInterval   = 30 * time.Second // between lag probes
	pingTimeout    = 90 * time.Second // an unanswered probe ends the connection
)

type line struct {
	at               time.Time
	nick, text, code string
	// client distinguishes local notes from server messages.
	client bool
}

// Each window keeps independent scrollback and activity.
type window struct {
	name, topic       string
	lines             []line
	nicks             map[string]string // folded nick to nick with its mode prefix
	unread, offset    int
	nickOff           int // first visible member in this channel
	pending           int // new lines received while scrolled back, measured on render
	mention, joined   bool
	namesReceiving    bool
	suppressJoinNames bool // hide an automatic NAMES reply (join or mode refresh) until its 366
	raw, server       bool
}

type connected struct {
	client *irc.Client
	config irc.Config
	err    error
	ctx    context.Context
	stop   func() bool // release cancellation cleanup after the UI takes ownership
}
type quitDone struct {
	client *irc.Client
	err    error
}
type incoming struct {
	client *irc.Client
	event  irc.Event
	ok     bool
}
type rawIn struct {
	client *irc.Client
	line   irc.Line
	ok     bool
}
type tick time.Time
type hit struct{ start, end, index int }
type nickHit struct {
	start, end, row int
	nick            string
}

// modePrefixes are the membership prefixes of a nick, highest rank first.
const modePrefixes = "~&@%+"

// membershipModes are the channel mode letters behind modePrefixes. Only a
// change to one of them alters the nicklist, so only it needs a NAMES refresh.
const membershipModes = "qaohv"

// isChannel reports whether a window name is a channel. v0.0.1 knows only # channels.
func isChannel(name string) bool { return strings.HasPrefix(name, "#") }

func newWindow(name string) *window { return &window{name: name, nicks: map[string]string{}} }

// Model is the whole interface state. Bubble Tea calls Update and View on one
// goroutine, so none of it is locked.
type Model struct {
	// Connection.
	Start      *irc.Config // connect here on start, when set
	client     *irc.Client
	leaving    *irc.Client // retained until the asynchronous QUIT finishes
	cancel     context.CancelFunc
	tmux       bool   // running inside tmux: graphics escapes need its passthrough envelope
	realname   string // sent in USER on the next connect; the nick when empty
	connectCtx context.Context
	conf       irc.Config
	connecting bool
	registered bool
	away       bool // the server confirmed we are away (RPL_NOWAWAY)
	quitting   bool
	nick       string
	network    string
	ping       string // token of the PING in flight, empty when none
	pingAt     time.Time
	lag        time.Duration
	now        time.Time

	// Windows.
	windows           []*window
	srv               *window // the server window, nil until the first connection
	nickWin           *window // where a pending /nick reports its result
	active, prevWin   int
	tabOff, windowOff int  // first visible status tab and windows-pane row
	revealWindows     bool // keep a newly selected window visible on the next draw
	view              viewMode
	listing           bool
	list              []listEntry
	listIdx           []int // channel list index of each visible row, -1 for none
	raw               []rawLine
	panelOff          int
	history           []string
	historyAt         int
	draft             string
	input             textinput.Model
	inputPalette      palette
	light             bool
	timestamps        bool
	nicklist          bool
	channels          bool
	sb, layMenu       bool
	width, height     int
	clCols, nlCols    int
	paneL, paneR      int
	drag, lastGrip    pane
	gripAt            time.Time
	sel               selection
	body              bodyGeometry
	nickHits          []nickHit
	nickWindow        int // the window nickHits were drawn for
	rowsVersion       uint64
	autoOpens         []time.Time     // when incoming messages last opened a window
	serverJoins       int             // joins the server made without a /join, this connection
	pendingJoins      map[string]bool // folded channels awaiting our JOIN echo
	refused           map[string]bool // folded channels whose server join was refused
	chatRows          messageCache
	nickList          nickCache
	debugRows         rawCache

	// Status bar images.
	gfx         graphics
	sprites     map[string]*sprite
	pngs        map[string][]byte
	pngsPending bool
	imagesUp    bool
	logoSent    bool
	logoPending bool
	logoDrawn   string
	logoGen     int
}

// New returns a disconnected model for nick, using the light palette when light is set.
func New(nick string, light bool) *Model {
	input := textinput.New()
	input.Prompt = "❯ "
	input.CharLimit = 500
	input.Focus()
	m := &Model{input: input, nick: nick, network: "offline", light: light, nicklist: true, timestamps: true, width: 100, height: 30, now: time.Now(), gfx: detectGraphics(os.Getenv), tmux: os.Getenv("TMUX") != "", sprites: map[string]*sprite{}, sb: true, clCols: chanlistW, nlCols: nicklistW}
	m.ensure("gloom")
	m.note(0, "Welcome to "+version.Name+". Type /help to get started.")
	return m
}

// Close cancels any connection attempt and closes the live connection.
func (m *Model) Close() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.client != nil {
		m.client.Close()
	}
	if m.leaving != nil {
		m.leaving.Close()
	}
}
func clockCmd() tea.Cmd { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tick(t) }) }
func wait(c *irc.Client) tea.Cmd {
	return func() tea.Msg { e, ok := <-c.Events; return incoming{c, e, ok} }
}
func waitRaw(c *irc.Client) tea.Cmd {
	return func() tea.Msg { l, ok := <-c.Raw; return rawIn{c, l, ok} }
}

// Init starts cursor blinking, the clock, image encoding, and any requested connection.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, clockCmd(), arrowPointer()}
	if m.gfx != gfxNone {
		cmds = append(cmds, m.renderSprites())
	}
	if m.Start != nil && m.nick != "" {
		cmds = append(cmds, m.connect(*m.Start))
		// connect has its own copy, so the start config drops the password.
		m.Start.SASL = irc.SASL{}
	}
	return tea.Batch(cmds...)
}

// Mouse reporting gives gloom control of selection, so use an arrow pointer.
// main restores the text pointer on exit; terminals without OSC 22 ignore it.
func arrowPointer() tea.Cmd { return tea.Raw(ansi.SetPointerShape("default")) }

func (m *Model) connect(c irc.Config) tea.Cmd {
	if m.connecting || m.client != nil || m.leaving != nil {
		m.note(m.active, "Already connected or connecting. Use /disconnect first.")
		return nil
	}
	m.resetServerLimits()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.connectCtx = ctx
	m.connecting = true
	i := m.openServer(c.Host)
	m.selectWindow(i)
	m.note(i, "Connecting to "+c.Host+":"+c.Port+"...")
	return func() tea.Msg {
		dialCtx, cancelDial := context.WithTimeout(ctx, connectTimeout)
		defer cancelDial()
		client, err := irc.Dial(dialCtx, c)
		c.SASL = irc.SASL{}
		result := connected{client: client, config: c, err: err, ctx: ctx}
		if client != nil {
			// A command's result can be discarded when Bubble Tea stops. Until
			// Update takes ownership, cancellation must also close the socket.
			result.stop = context.AfterFunc(ctx, client.Close)
		}
		return result
	}
}
func (m *Model) ensure(name string) int {
	name = irc.Clean(name)
	if i := m.find(name); i >= 0 {
		return i
	}
	w := newWindow(name)
	// As in the prototype, channels come before private messages and the debug
	// window stays last. The server window sits ahead of both.
	at := len(m.windows)
	if at > 0 && m.windows[at-1].raw {
		at--
	}
	if isChannel(name) {
		for i := 1; i < at; i++ {
			if !isChannel(m.windows[i].name) && !m.windows[i].server {
				at = i
				break
			}
		}
	}
	return m.insert(w, at)
}
func (m *Model) insert(w *window, at int) int {
	// Preserve the first visible windows when an arrival inserts before them.
	if at > 0 && m.tabOff >= at {
		m.tabOff++
	}
	if at > 0 && m.windowOff >= at {
		m.windowOff++
	}
	m.windows = slices.Insert(m.windows, at, w)
	if m.active >= at && len(m.windows) > 1 {
		m.active++
	}
	if m.prevWin >= at {
		m.prevWin++
	}
	return at
}

// openServer returns the window for the server being connected to. Gloom keeps
// one server, so a window left open by an earlier connection is renamed and
// reused with its history. A new one sits right after the gloom window.
func (m *Model) openServer(name string) int {
	if i := slices.Index(m.windows, m.srv); i >= 0 {
		m.srv.name = name
		return i
	}
	m.srv = newWindow(name)
	m.srv.server = true
	return m.insert(m.srv, 1)
}

// serverWindow is where server notices go: the current server's window, or the
// gloom window when there is none.
func (m *Model) serverWindow() int { return max(0, slices.Index(m.windows, m.srv)) }

func (m *Model) current() *window { return m.windows[m.active] }

// showing reports whether window i fills the main area. An overlay hides it.
func (m *Model) showing(i int) bool { return m.view == viewWindow && i == m.active }

// mentions reports whether text contains a nick already folded for IRC comparison.
func mentions(text, foldedNick string) bool {
	return foldedNick != "" && strings.Contains(irc.Fold(text), foldedNick)
}

func (m *Model) add(i int, nick, text string) { m.addCode(i, "", nick, text) }

// Local notes use the sent arrow; server lines use the received arrow.
func (m *Model) note(i int, text string) { m.addLine(i, line{text: text, client: true}) }

// addCode adds a line that answers a numeric reply. The code replaces the
// gutter glyph.
func (m *Model) addCode(i int, code, nick, text string) {
	m.addLine(i, line{nick: nick, text: text, code: code})
}

func (m *Model) addLine(i int, l line) {
	m.rowsVersion++
	w := m.windows[i]
	l.at = time.Now()
	l.nick, l.text = irc.Clean(l.nick), irc.Clean(l.text)
	w.lines = append(w.lines, l)
	if w.offset > 0 {
		w.offset++
		w.pending++
	}
	if len(w.lines) > maxLines {
		// Release evicted text while the backing array is still retained.
		// Reslicing avoids copying the remaining history on every arrival.
		dropped := len(w.lines) - maxLines
		clear(w.lines[:dropped])
		w.lines = w.lines[dropped:]
		if m.sel.win == w {
			m.sel = selection{}
		}
	}
	if i == m.active {
		return
	}
	w.unread++
	if mentions(l.text, irc.Fold(m.nick)) {
		w.mention = true
	}
}

// closeWindow removes window i, keeping the active and previous windows
// pointing at the same windows. The gloom window cannot be closed.
func (m *Model) closeWindow(i int) {
	if i <= 0 || i >= len(m.windows) {
		return
	}
	if m.sel.win == m.windows[i] {
		m.sel = selection{}
	}
	m.nickHits = nil
	if i < m.tabOff {
		m.tabOff--
	}
	if i < m.windowOff {
		m.windowOff--
	}
	m.windows = slices.Delete(m.windows, i, i+1)
	switch {
	case m.active == i:
		m.selectWindow(i - 1)
	case m.active > i:
		m.active--
	}
	switch {
	case m.prevWin == i:
		m.prevWin = 0
	case m.prevWin > i:
		m.prevWin--
	}
}
func (m *Model) selectWindow(i int) {
	if i < 0 || i >= len(m.windows) {
		return
	}
	m.view, m.layMenu = viewWindow, false
	m.active = i
	m.revealWindows = true
	m.windows[i].unread = 0
	m.windows[i].mention = false
}
func (m *Model) send(s string) bool {
	if m.client == nil {
		m.note(m.active, notConnected)
		return false
	}
	if err := m.client.Send(s); err != nil {
		m.note(m.active, err.Error())
		return false
	}
	return true
}

const notConnected = "Not connected."

// Update handles one message and returns the model with the next command.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	return next, tea.Batch(cmd, m.scheduleLogo())
}
func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, nil
	}
	switch v := msg.(type) {
	case spritesReady:
		if m.pngs == nil {
			m.pngs = make(map[string][]byte, len(v))
		}
		maps.Copy(m.pngs, v)
		m.pngsPending = false
		return m, nil
	case logoDraw:
		if v.gen != m.logoGen {
			return m, nil
		}
		m.logoPending = false
		if m.imagesWanted() && !m.themeReady() {
			return m, nil
		}
		return m, m.drawLogo()
	case tea.WindowSizeMsg:
		m.revealWindows = true
		m.resetLogo()
		m.nickHits = nil
		m.width = v.Width
		m.height = v.Height
		m.input.SetWidth(max(1, v.Width-3))
	case connected:
		if v.ctx == nil || v.ctx != m.connectCtx || v.ctx.Err() != nil {
			if v.client != nil {
				v.client.Close()
			}
			return m, nil
		}
		if v.stop != nil {
			v.stop()
		}
		m.connecting = false
		if v.err != nil {
			m.cancel()
			m.cancel, m.connectCtx = nil, nil
			m.note(m.serverWindow(), "Connection failed: "+v.err.Error())
			return m, nil
		}
		m.client = v.client
		m.network = v.config.Host
		m.conf = v.config
		// The client holds the credentials for negotiation. The model
		// keeps no copy.
		m.conf.SASL = irc.SASL{}
		m.registered = false
		m.away = false
		m.note(m.serverWindow(), "Transport connected; registering...")
		return m, tea.Batch(wait(v.client), waitRaw(v.client))
	case quitDone:
		if v.client != m.leaving {
			return m, nil
		}
		m.leaving = nil
		if v.err != nil {
			m.note(m.serverWindow(), "Quit failed: "+v.err.Error())
		}
		return m, nil
	case rawIn:
		if v.client != m.client || !v.ok {
			return m, nil
		}
		kind := rawReceived
		if v.line.Out {
			kind = rawSent
		}
		m.logRaw(kind, v.line.Text)
		return m, waitRaw(v.client)
	case incoming:
		if v.client != m.client {
			return m, nil
		}
		if !v.ok || v.event.Err != nil {
			if v.event.Err != nil {
				m.note(m.serverWindow(), v.event.Err.Error())
			}
			m.disconnect()
			return m, nil
		}
		if v.event.Notice != "" {
			m.note(m.serverWindow(), v.event.Notice)
			return m, wait(v.client)
		}
		m.receive(v.event)
		return m, wait(v.client)
	case tick:
		m.now = time.Time(v)
		if m.registered && m.ping == "" && (m.pingAt.IsZero() || m.now.Sub(m.pingAt) > pingInterval) {
			m.ping = strconv.FormatInt(m.now.UnixNano(), 10)
			m.pingAt = m.now
			m.send("PING :" + m.ping)
		}
		if m.ping != "" && m.now.Sub(m.pingAt) > pingTimeout {
			m.note(m.serverWindow(), "Server ping timed out.")
			m.disconnect()
		}
		return m, clockCmd()
	case tea.MouseMotionMsg:
		m.dragTo(v.X)
		m.extendSelect(v.X, v.Y)
		return m, nil
	case tea.MouseReleaseMsg:
		m.drag = paneNone
		return m, m.endSelect()
	case tea.MouseWheelMsg:
		m.scrollMouse(v)
		return m, nil
	case tea.MouseClickMsg:
		if v.X < 0 || v.X >= m.width || v.Y < 0 || v.Y >= m.height {
			return m, nil
		}
		return m, m.click(v)
	case tea.KeyPressMsg:
		if cmd, handled := m.keyPress(v.String()); handled {
			return m, cmd
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// keyPress handles a key. It reports false when the key is not gloom's, so the
// input field gets it.
func (m *Model) keyPress(key string) (tea.Cmd, bool) {
	if m.view == viewSplash && !strings.HasPrefix(key, "shift") && key != "ctrl+c" {
		m.view = viewWindow
		if key != "/" {
			return nil, true
		}
	}
	switch key {
	case "esc":
		// Esc closes an open overlay first, and only then drops a selection.
		if !m.closeOverlays() {
			m.sel = selection{}
		}
	case "ctrl+c":
		m.Close()
		return tea.Quit, true
	case "pgup":
		m.scroll(m.pageRows())
	case "pgdown":
		m.scroll(-m.pageRows())
	case "up":
		m.historyStep(-1)
	case "down":
		m.historyStep(1)
	case "tab":
		m.complete()
	case "enter":
		return m.submit(), true
	default:
		return nil, false
	}
	return nil, true
}

// pageRows is how far PgUp and PgDn scroll: the message area less one row.
func (m *Model) pageRows() int { return max(1, m.height-2-inputPadRows-bottomRows-1) }

// historyStep moves through the input history, keeping the unsent draft.
func (m *Model) historyStep(dir int) {
	next := m.historyAt + dir
	if next < 0 || next > len(m.history) {
		return
	}
	if m.historyAt == len(m.history) {
		m.draft = m.input.Value()
	}
	m.historyAt = next
	text := m.draft
	if next < len(m.history) {
		text = m.history[next]
	}
	m.input.SetValue(text)
	m.input.CursorEnd()
}

// submit sends the input line to wherever the current view wants it.
func (m *Model) submit() tea.Cmd {
	s := strings.TrimSpace(m.input.Value())
	m.input.SetValue("")
	if s == "" {
		return nil
	}
	// Connection options may contain credentials, including malformed input.
	// Keep the whole command out of recall rather than retaining a password.
	fields := strings.Fields(s)
	verb := strings.ToLower(fields[0])
	connection := verb == "/connect" || verb == "/server"
	if !connection {
		m.history = append(m.history, s)
		if len(m.history) > maxHistory {
			m.history = slices.Delete(m.history, 0, 1)
		}
	}
	m.historyAt = len(m.history)
	m.draft = ""
	// Connection and exit commands also run locally in the raw traffic window.
	if connection {
		return m.command(strings.Join(fields, " "))
	}
	verb, _, _ = strings.Cut(s, " ")
	if strings.EqualFold(verb, "/exit") || strings.EqualFold(verb, "/quit") {
		return m.command(s)
	}
	command := strings.HasPrefix(s, "/")
	switch {
	case m.view == viewList && !command:
		m.listSubmit(s)
	case m.view == viewConfig && !command:
	case m.showing(m.active) && m.current().raw:
		m.rawInput(s)
	default:
		return m.command(s)
	}
	return nil
}

// wheelRows is how many rows one mouse wheel notch scrolls.
const wheelRows = 3

// scrollMouse routes a wheel event to the pane under the pointer,
// or to the current view. Pane scrolling never changes chat scrollback.
func (m *Model) scrollMouse(v tea.MouseWheelMsg) {
	if !m.fitsLayout() || v.X < 0 || v.X >= m.width || v.Y < 0 || v.Y >= m.height {
		return
	}
	if v.Y >= m.height-statusRows {
		dir := wheelDir(v.Button, true)
		if dir == 0 || !m.statusWindows() {
			return
		}
		m.tabOff = max(1, min(max(1, m.tabOff)+dir, len(m.windows)-1))
		m.revealWindows = false
		m.status() // Clamp against the rendered tab widths.
		return
	}
	dir := wheelDir(v.Button, false)
	if dir == 0 {
		return
	}
	if m.paneL > 0 && v.X < m.paneL {
		visible := max(1, m.height-statusRows-1)
		m.windowOff = max(0, min(m.windowOff+dir*wheelRows, len(m.windows)-visible))
		m.revealWindows = false
		return
	}
	if m.paneR > 0 && v.X >= m.width-m.paneR {
		w := m.current()
		visible := max(1, m.height-statusRows-1)
		w.nickOff = max(0, min(w.nickOff+dir*wheelRows, len(w.nicks)-visible))
		return
	}
	m.scroll(-dir * wheelRows)
}

// wheelDir is the direction a wheel button moves a list: -1 back, 1 forward
// and 0 for none. Vertical lists follow only up and down. A trackpad swipe
// mixes sideways events into a vertical one, and counting those as vertical
// made lists jump back and forth. The horizontal status tabs set across to
// also follow left and right.
func wheelDir(b tea.MouseButton, across bool) int {
	switch b {
	case tea.MouseWheelUp:
		return -1
	case tea.MouseWheelDown:
		return 1
	case tea.MouseWheelLeft:
		if across {
			return -1
		}
	case tea.MouseWheelRight:
		if across {
			return 1
		}
	}
	return 0
}

// scroll moves the visible view by rows: positive towards older content,
// negative towards newer. View clamps the upper bound.
func (m *Model) scroll(rows int) {
	if m.view == viewList || m.view == viewConfig {
		m.panelOff = max(0, m.panelOff-rows)
		return
	}
	w := m.current()
	w.offset = max(0, w.offset+rows)
}
func (m *Model) disconnect() {
	m.pendingJoins = nil
	m.rowsVersion++
	m.Close()
	m.cancel, m.connectCtx = nil, nil
	m.connecting = false
	m.client = nil
	m.leaving = nil
	m.network = "offline"
	m.nickWin = nil
	m.registered = false
	m.away = false
	m.ping = ""
	m.lag = 0
	m.pingAt = time.Time{}
	m.listing = false
	m.list, m.listIdx = nil, nil
	for _, w := range m.windows {
		w.joined = false
		w.namesReceiving = false
		w.suppressJoinNames = false
		clear(w.nicks)
	}
}
