package ui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"gloom.im/gloom/internal/irc"
)

var commandNames = []string{
	"/connect", "/server", "/join", "/part", "/nick", "/msg", "/query", "/quit", "/exit", "/help", "/theme",
	"/disconnect", "/close", "/whois", "/whowas", "/names", "/list", "/who", "/notice", "/topic", "/kick",
	"/leave", "/privmsg", "/clear", "/splash", "/mode", "/invite", "/motd", "/away", "/config", "/quote",
}

func (m *Model) command(s string) tea.Cmd {
	if !strings.HasPrefix(s, "/") {
		m.message(m.current().name, s)
		return nil
	}
	cmd, rest, _ := strings.Cut(s, " ")
	rest = strings.TrimSpace(rest)
	args := strings.Fields(rest)
	need := func(n int) bool {
		if len(args) < n {
			m.note(m.active, "Missing argument. Type /help.")
			return false
		}
		return true
	}
	name := strings.ToLower(cmd)
	if name == "/quote" {
		// Raw data is sent as typed, so it skips the trimmed rest.
		_, data, _ := strings.Cut(s, " ")
		m.quote(strings.TrimLeft(data, " "))
		return nil
	}
	if m.ircCommand(name, rest) {
		return nil
	}
	switch name {
	case "/help":
		m.note(m.active, "/connect [-TLS] [-sasl-user account -sasl-pass password [-sasl-mech PLAIN]] host [port] | /server | /disconnect | /join #channel | /part [#channel] [reason] | /nick name | /query nick | /msg nick text | /theme [dark|light] | /config realname \"value\" | /close [-c | -m] (-c all channels, -m all messages) |/quit [reason] (leave the server) | /exit [reason] (leave gloom)")
		m.note(m.active, "/whois nick | /whowas nick [count [server]] | /names [#channel] | /list [#channel] | /who [mask] | /notice target text | /topic [#channel] [text] | /kick [#channel] nick [reason] | /leave (alias /part) | /privmsg (alias /msg)")
		m.note(m.active, "/clear | /mode [target] [modes [args]] | /invite nick [#channel] | /motd [server] | /away [message] (no message clears away) | /quote data (send raw data to the server)")
		m.note(m.active, "/splash | PgUp/PgDn or wheel scroll | drag to copy | Tab complete | Up/Down history | Ctrl+C quit")
	case "/connect", "/server":
		if m.nick == "" {
			m.note(m.active, "Set a nickname first: /nick name")
			return nil
		}
		c, err := irc.ParseAddress(args, m.nick)
		if err != nil {
			m.note(m.active, err.Error())
			return nil
		}
		c.Realname = m.realname
		return m.connect(c)
	case "/config":
		m.config(rest)
	case "/splash":
		m.layMenu = false
		if m.view == viewSplash {
			m.view = viewWindow
		} else {
			m.view = viewSplash
		}
	case "/disconnect":
		m.disconnect()
	case "/clear":
		if rest != "" {
			m.note(m.active, "usage: /clear")
			return nil
		}
		w := m.current()
		m.rowsVersion++
		w.lines = nil
		w.offset = 0
		w.pending = 0
		m.sel = selection{}
		w.unread = 0
		w.mention = false
	case "/quit", "/exit":
		reason := strings.TrimPrefix(rest, ":")
		if !irc.ValidLine("QUIT :" + reason) {
			m.note(m.active, "Invalid quit reason: too long or contains line breaks.")
			return nil
		}
		if name == "/quit" {
			return m.quit(reason)
		}
		return m.exit(reason)
	case "/join":
		if !need(1) || !validChannel(args[0]) {
			m.note(m.active, "usage: /join #channel")
			return nil
		}
		m.joinChannel(args[0])
	case "/part", "/leave":
		target := m.current().name
		reason := rest
		if len(args) > 0 && isChannel(args[0]) {
			target = args[0]
			_, reason, _ = strings.Cut(rest, " ")
		}
		if !validChannel(target) {
			m.note(m.active, "Select a channel first.")
			return nil
		}
		m.send("PART " + target + " :" + reason)
	case "/nick":
		if !need(1) || !irc.ValidNick(args[0]) {
			m.note(m.active, "Invalid nickname.")
			return nil
		}
		m.changeNick(args[0])
	case "/query":
		if !need(1) || !irc.ValidNick(args[0]) {
			return nil
		}
		m.selectWindow(m.ensure(args[0]))
	case "/msg", "/privmsg":
		target, text, ok := strings.Cut(rest, " ")
		if !ok {
			m.note(m.active, "usage: /msg target message")
			return nil
		}
		m.message(target, text)
	case "/theme":
		switch rest {
		case "light":
			m.light = true
		case "dark":
			m.light = false
		default:
			m.light = !m.light
		}
	case "/close":
		return m.closeCommand(rest)
	default:
		m.unknownCommand(cmd)
	}
	return nil
}

// closeCommand handles /close. Alone it closes the current window, and in the
// server window it also leaves the server. -c closes every channel window and
// -m every private message window. The gloom window always stays open.
func (m *Model) closeCommand(rest string) tea.Cmd {
	switch rest {
	case "-c":
		m.closeWhere(func(w *window) bool { return isChannel(w.name) })
		return nil
	case "-m":
		m.closeWhere(func(w *window) bool { return !isChannel(w.name) })
		return nil
	case "":
	default:
		m.note(m.active, "usage: /close [-c | -m]")
		return nil
	}
	if m.active == 0 {
		m.note(m.active, "The gloom window cannot be closed.")
		return nil
	}
	if m.current().server {
		return m.closeServer()
	}
	m.closeChat(m.active)
	return nil
}

// closeWhere closes every channel or private message window that keep
// selects. The gloom, server and debug windows are never passed to keep.
func (m *Model) closeWhere(keep func(*window) bool) {
	for i := len(m.windows) - 1; i > 0; i-- {
		w := m.windows[i]
		if w.server || w.raw || !keep(w) {
			continue
		}
		m.closeChat(i)
	}
}

// closeChat closes window i, leaving its channel first when joined.
func (m *Model) closeChat(i int) {
	if w := m.windows[i]; w.joined {
		m.send("PART " + w.name)
	}
	m.closeWindow(i)
}

// closeServer leaves the server and closes its window with every channel and
// private message window, landing on the gloom window. The debug window stays.
func (m *Model) closeServer() tea.Cmd {
	for i := len(m.windows) - 1; i > 0; i-- {
		if m.windows[i].raw {
			continue
		}
		m.closeWindow(i)
	}
	m.srv = nil
	m.selectWindow(0)
	if m.client != nil {
		return m.quit("")
	}
	if m.connecting {
		m.disconnect()
		m.note(0, "Connection cancelled.")
	}
	return nil
}

func (m *Model) unknownCommand(s string) { m.note(m.active, "Unknown command: "+s+". Type /help.") }

// changeNick waits for the server echo when connected, or sets the local nick.
func (m *Model) changeNick(nick string) {
	if m.client != nil {
		if !m.send("NICK " + nick) {
			return
		}
		m.nickWin = m.current()
		return
	}
	if m.nick == "" {
		m.note(m.active, "Nickname set to "+nick)
	} else {
		m.add(m.active, "", m.nick+" is now "+nick)
	}
	m.nick = nick
}

// quit leaves the server but keeps gloom and every window open.
func (m *Model) quit(reason string) tea.Cmd {
	if m.client == nil {
		m.note(m.active, notConnected)
		return nil
	}
	client := m.client
	m.client = nil
	m.disconnect()
	m.leaving = client
	m.note(m.serverWindow(), "Disconnected from "+m.conf.Host+".")
	return func() tea.Msg { return quitDone{client: client, err: client.Quit(reason)} }
}

// exit leaves gloom, after telling the server when connected.
func (m *Model) exit(reason string) tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	if m.client == nil {
		m.Close()
		return tea.Quit
	}
	m.quitting = true
	client := m.client
	return func() tea.Msg { _ = client.Quit(reason); return tea.Quit() }
}

func (m *Model) message(target, text string) {
	// The gloom and server windows have nobody to talk to, so text typed there
	// can only have been meant as a command.
	if i := m.find(target); i == 0 || (i > 0 && m.windows[i].server) {
		m.unknownCommand(text)
		return
	}
	if !irc.Atom(target) || text == "" {
		m.note(m.active, "Select a channel or use /query nick.")
		return
	}
	if isChannel(target) && !m.windows[m.ensure(target)].joined {
		m.note(m.active, "Join the channel before sending.")
		return
	}
	if !m.send("PRIVMSG " + target + " :" + text) {
		return
	}
	m.add(m.ensure(target), m.nick, text)
}
func (m *Model) complete() {
	runes := []rune(m.input.Value())
	cursor := m.input.Position()
	before := string(runes[:cursor])
	pos := strings.LastIndex(before, " ") + 1
	prefix := before[pos:]
	end := cursor
	for end < len(runes) && runes[end] != ' ' {
		end++
	}
	choices := slices.Clone(commandNames)
	for _, w := range m.windows {
		choices = append(choices, w.name)
	}
	for _, n := range m.current().nicks {
		choices = append(choices, strings.TrimLeft(n, modePrefixes))
	}
	slices.Sort(choices)
	folded := irc.Fold(prefix)
	for _, c := range choices {
		if !strings.HasPrefix(irc.Fold(c), folded) || c == prefix {
			continue
		}
		suffix := " "
		if pos == 0 && !strings.HasPrefix(c, "/") && !isChannel(c) {
			suffix = ": "
		}
		if end < len(runes) {
			suffix = ""
		}
		replacement := before[:pos] + c + suffix
		m.input.SetValue(replacement + string(runes[end:]))
		m.input.SetCursor(len([]rune(replacement)))
		return
	}
}

// config handles /config. Only "realname" exists. The value is kept for this
// run only, takes effect on the next connect, and is never saved.
func (m *Model) config(rest string) {
	key, value, _ := strings.Cut(rest, " ")
	if !strings.EqualFold(key, "realname") {
		m.note(m.active, `Usage: /config realname "value" (an empty value sends the nickname)`)
		return
	}
	value = strings.TrimSpace(value)
	if unquoted, ok := strings.CutPrefix(value, `"`); ok {
		unquoted, ok = strings.CutSuffix(unquoted, `"`)
		if !ok {
			m.note(m.active, "Missing closing quote.")
			return
		}
		value = unquoted
	}
	if value == "" && !strings.Contains(rest, `""`) {
		shown := m.realname
		if shown == "" {
			shown = "(the nickname)"
		}
		m.note(m.active, "realname is "+shown)
		return
	}
	if value != "" && !irc.ValidRealname(value) {
		m.note(m.active, "Invalid realname: at most 100 bytes, no control characters.")
		return
	}
	m.realname = value
	m.note(m.active, "realname set. It applies the next time you connect and is not saved.")
}
