package ui

import (
	"slices"
	"strconv"
	"strings"

	"gloom.im/gloom/internal/irc"
)

// atoms reports whether every comma separated item of every value is safe to
// put in a parameter.
func atoms(values []string) bool {
	for _, value := range values {
		for item := range strings.SplitSeq(value, ",") {
			if !irc.Atom(item) {
				return false
			}
		}
	}
	return true
}

func validChannel(s string) bool { return isChannel(s) && irc.Atom(s) }

func ircLine(verb string, args ...string) string {
	if len(args) == 0 {
		return verb
	}
	return verb + " " + strings.Join(args, " ")
}

// ircCommand handles server commands without interpreting free text as options.
func (m *Model) ircCommand(cmd, rest string) bool {
	args := strings.Fields(rest)
	usage := func(s string) { m.note(m.active, "usage: "+s) }
	current := m.current().name
	switch cmd {
	case "/mode":
		target := current
		if !validChannel(target) {
			target = m.nick
		}
		if len(args) > 0 && !strings.HasPrefix(args[0], "+") && !strings.HasPrefix(args[0], "-") {
			target = args[0]
			args = args[1:]
		}
		if !irc.Atom(target) || !atoms(args) {
			usage("/mode [target] [modes [arguments]]")
			return true
		}
		m.send(ircLine("MODE", slices.Insert(args, 0, target)...))
	case "/invite":
		target := current
		if len(args) == 2 {
			target = args[1]
		}
		if len(args) < 1 || len(args) > 2 || !irc.ValidNick(args[0]) || !validChannel(target) {
			usage("/invite nick [#channel]")
			return true
		}
		m.send("INVITE " + args[0] + " " + target)
	case "/motd":
		if len(args) > 1 || !atoms(args) {
			usage("/motd [server]")
			return true
		}
		m.send(ircLine("MOTD", args...))
	case "/away":
		// As in irssi, a message sets or replaces the away status and a bare
		// /away clears it. The 305 and 306 replies update the title.
		line := "AWAY"
		if rest != "" {
			line += " :" + strings.TrimPrefix(rest, ":")
		}
		m.send(line)
	case "/whois":
		if len(args) < 1 || len(args) > 2 || !atoms(args) {
			usage("/whois [server] nick[,nick]")
			return true
		}
		m.send(ircLine("WHOIS", args...))
	case "/whowas":
		valid := len(args) >= 1 && len(args) <= 3 && atoms(args)
		if len(args) >= 2 {
			n, err := strconv.Atoi(args[1])
			valid = valid && err == nil && n >= 0
		}
		if !valid {
			usage("/whowas nick[,nick] [count [server]]")
			return true
		}
		m.send(ircLine("WHOWAS", args...))
	case "/names", "/list", "/who":
		if cmd == "/list" && len(args) == 0 {
			m.toggleList()
			return true
		}
		if len(args) > 2 || !atoms(args) {
			usage(cmd + " [target] [server or flags]")
			return true
		}
		if cmd == "/names" && len(args) == 0 && validChannel(current) {
			args = []string{current}
		}
		if cmd == "/who" && len(args) == 0 {
			target := "0"
			if validChannel(current) {
				target = current
			}
			args = []string{target}
		}
		m.send(ircLine(strings.ToUpper(strings.TrimPrefix(cmd, "/")), args...))
	case "/notice":
		target, text, ok := strings.Cut(rest, " ")
		text = strings.TrimSpace(text)
		if !ok || !irc.Atom(target) || text == "" {
			usage("/notice target text")
			return true
		}
		if m.send("NOTICE " + target + " :" + text) {
			m.note(m.active, "-> notice "+target+": "+text)
		}
	case "/topic":
		target, text := current, rest
		explicitText := rest != ""
		if isChannel(rest) {
			var hasText bool
			target, text, hasText = strings.Cut(rest, " ")
			text = strings.TrimSpace(text)
			explicitText = hasText && text != ""
		}
		if !validChannel(target) {
			usage("/topic [#channel] [text] (use : to clear)")
			return true
		}
		line := "TOPIC " + target
		if explicitText {
			text = strings.TrimPrefix(text, ":")
			line += " :" + text
		}
		m.send(line)
	case "/kick":
		target := current
		if isChannel(rest) {
			target, rest, _ = strings.Cut(rest, " ")
			rest = strings.TrimSpace(rest)
		}
		nick, reason, _ := strings.Cut(rest, " ")
		if !validChannel(target) || !irc.ValidNick(nick) {
			usage("/kick [#channel] nick [reason]")
			return true
		}
		line := "KICK " + target + " " + nick
		if reason != "" {
			line += " :" + strings.TrimPrefix(reason, ":")
		}
		m.send(line)
	default:
		return false
	}
	return true
}
