package ui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"gloom.im/gloom/internal/irc"
)

// nickRefusals are the numeric replies that mean a nick change will never be echoed.
var nickRefusals = []string{"431", "432", "433", "436", "437", "438"}

func isNumeric(command string) bool {
	return len(command) == 3 && strings.Trim(command, "0123456789") == ""
}

func (m *Model) receive(e irc.Event) {
	p := e.Message
	if p == nil {
		return
	}
	// addLine and disconnect invalidate the row cache. Nick changes also add
	// a line; replies that leave message rows unchanged must not invalidate it.
	a := p.Params
	// A numeric reply starts with the target, our own nick, which is not shown.
	code := ""
	if isNumeric(p.Command) {
		code = p.Command
	}
	nick := irc.Clean(p.Name)
	folded := irc.Fold(nick)
	self := folded == irc.Fold(m.nick)
	if m.listing && (p.Command == "321" || p.Command == "322" || p.Command == "323") {
		m.receiveList(p.Command, a)
		return
	}
	if slices.Contains(nickRefusals, p.Command) {
		m.nickWin = nil
	}
	switch p.Command {
	case "001":
		m.registered = true
		m.nickWin = nil
		if len(a) > 0 {
			m.nick = irc.Clean(a[0])
		}
		m.addCode(m.serverWindow(), code, "", p.Trailing())
	case "005":
		for _, v := range a {
			if n, ok := strings.CutPrefix(v, "NETWORK="); ok {
				m.network = irc.Clean(n)
			}
		}
	case "PONG":
		if p.Trailing() == m.ping && m.ping != "" {
			m.lag = time.Since(m.pingAt)
			m.ping = ""
		}
	case "PRIVMSG", "NOTICE":
		if len(a) < 2 {
			return
		}
		// CTCP is unsupported. Ignore it before opening windows or recording activity.
		if strings.ContainsRune(a[1], '\x01') {
			return
		}
		target := a[0]
		if irc.Fold(target) == irc.Fold(m.nick) {
			target = nick
		}
		i := m.serverWindow()
		// A message for a window that does not exist opens one only within
		// the limit. Past it, the message is shown in the server window.
		overflow := false
		if p.Command == "PRIVMSG" || isChannel(target) {
			switch j := m.find(irc.Clean(target)); {
			case j >= 0:
				i = j
			case m.refused[irc.Fold(irc.Clean(target))] || !m.allowAutoWindow():
				overflow = true
			default:
				i = m.ensure(target)
			}
		}
		text := a[1]
		if overflow && isChannel(target) {
			text = target + ": " + text
		}
		m.add(i, nick, text)
	case "JOIN":
		if len(a) < 1 {
			return
		}
		// A join for a channel with no window was not asked for: /join opens
		// its window first. The server may do that only a few times.
		if self && m.find(irc.Clean(a[0])) < 0 && !m.allowServerJoin() {
			m.refuse(a[0])
			m.note(m.serverWindow(), "Ignored a server join to "+a[0]+": the limit of "+strconv.Itoa(maxServerJoins)+" per connection is reached.")
			return
		}
		i, ok := m.channelWindow(a[0])
		if !ok {
			return
		}
		w := m.windows[i]
		w.nicks[folded] = nick
		if self {
			w.joined = true
			w.suppressJoinNames = true
			m.selectWindow(i)
		}
		m.add(i, "", nick+" joined")
	case "PART", "KICK":
		if len(a) < 1 {
			return
		}
		who, reason := nick, ""
		switch {
		case p.Command == "KICK":
			if len(a) > 1 {
				who = a[1]
			}
			if len(a) > 2 {
				reason = a[2]
			}
		case len(a) > 1:
			reason = a[1]
		}
		if p.Command == "PART" && self {
			m.closeWindow(m.find(a[0]))
			return
		}
		i, ok := m.channelWindow(a[0])
		if !ok {
			return
		}
		gone := irc.Fold(who)
		delete(m.windows[i].nicks, gone)
		if gone == irc.Fold(m.nick) {
			m.windows[i].joined = false
			clear(m.windows[i].nicks)
		}
		text := who + " left"
		if reason != "" {
			text += ": " + reason
		}
		m.add(i, "", text)
	case "QUIT":
		for i, w := range m.windows {
			if _, ok := w.nicks[folded]; !ok {
				continue
			}
			delete(w.nicks, folded)
			m.add(i, "", nick+" quit: "+p.Trailing())
		}
	case "NICK":
		if len(a) < 1 {
			return
		}
		next := irc.Clean(a[0])
		for _, w := range m.windows {
			if old, ok := w.nicks[folded]; ok {
				delete(w.nicks, folded)
				prefix, _ := splitPrefix(old)
				w.nicks[irc.Fold(next)] = prefix + next
			}
			if irc.Fold(w.name) == folded {
				w.name = next
			}
		}
		i := m.serverWindow()
		if self {
			m.nick = next
			// Report our own change in the window it was asked for.
			if j := slices.Index(m.windows, m.nickWin); j >= 0 {
				i = j
			}
			m.nickWin = nil
		}
		m.add(i, "", nick+" is now "+next)
	case "353":
		if len(a) < 4 {
			return
		}
		i, ok := m.channelWindow(a[2])
		if !ok {
			return
		}
		w := m.windows[i]
		if !w.namesReceiving {
			clear(w.nicks)
			w.namesReceiving = true
		}
		for n := range strings.FieldsSeq(a[3]) {
			n = irc.Clean(n)
			w.nicks[irc.Fold(strings.TrimLeft(n, modePrefixes))] = n
		}
		if !w.suppressJoinNames {
			m.addCode(i, code, "", "Names: "+a[3])
		}
	case "366":
		if len(a) < 2 {
			return
		}
		i, ok := m.channelWindow(a[1])
		if !ok {
			return
		}
		m.windows[i].namesReceiving = false
		m.windows[i].suppressJoinNames = false
	case "331":
		if len(a) < 2 {
			return
		}
		i, ok := m.channelWindow(a[1])
		if !ok {
			return
		}
		m.windows[i].topic = ""
		m.addCode(i, code, "", "No topic is set.")
	case "332":
		if len(a) < 3 {
			return
		}
		i, ok := m.channelWindow(a[1])
		if !ok {
			return
		}
		m.windows[i].topic = irc.Clean(a[2])
		m.addCode(i, code, "", "Topic: "+a[2])
	case "TOPIC":
		if len(a) < 2 {
			return
		}
		i, ok := m.channelWindow(a[0])
		if !ok {
			return
		}
		m.windows[i].topic = irc.Clean(a[1])
	case "MODE":
		if len(a) < 2 || !isChannel(a[0]) {
			return
		}
		i, ok := m.channelWindow(a[0])
		if !ok {
			return
		}
		m.add(i, "", nick+" set mode "+strings.Join(a[1:], " "))
		m.send("NAMES " + a[0])
	case "ERROR":
		m.add(m.serverWindow(), "", p.Trailing())
		m.disconnect()
	default:
		if code != "" && len(a) > 0 {
			a = a[1:]
		}
		m.addCode(m.serverWindow(), code, "", strings.Join(a, " "))
	}
}
