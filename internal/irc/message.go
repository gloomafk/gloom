package irc

import (
	"errors"
	"strings"
)

// Message is one parsed server line: [@tags] [:prefix] COMMAND [params] [:trailing].
type Message struct {
	// Name is the nick or server name from the prefix, empty when there is none.
	Name    string
	Command string
	// Params holds every parameter, with the trailing one last.
	Params []string
}

// ParseMessage parses one protocol line without its CRLF. IRCv3 message tags
// are skipped, since v0.0.1 does not use them.
func ParseMessage(line string) (*Message, error) {
	line = strings.TrimRight(line, "\r\n")
	if strings.HasPrefix(line, "@") {
		_, rest, ok := strings.Cut(line, " ")
		if !ok {
			return nil, errors.New("malformed message: tags without command")
		}
		line = strings.TrimLeft(rest, " ")
	}
	m := &Message{}
	if strings.HasPrefix(line, ":") {
		prefix, rest, ok := strings.Cut(line[1:], " ")
		if !ok || prefix == "" {
			return nil, errors.New("malformed message: bad prefix")
		}
		m.Name, _, _ = strings.Cut(prefix, "!")
		m.Name, _, _ = strings.Cut(m.Name, "@")
		line = strings.TrimLeft(rest, " ")
	}
	command, rest, _ := strings.Cut(line, " ")
	if command == "" {
		return nil, errors.New("malformed message: no command")
	}
	m.Command = strings.ToUpper(command)
	for rest = strings.TrimLeft(rest, " "); rest != ""; rest = strings.TrimLeft(rest, " ") {
		if trailing, ok := strings.CutPrefix(rest, ":"); ok {
			m.Params = append(m.Params, trailing)
			break
		}
		param, next, _ := strings.Cut(rest, " ")
		m.Params = append(m.Params, param)
		rest = next
	}
	return m, nil
}

// Trailing returns the last parameter, or "" when there are none.
func (m *Message) Trailing() string {
	if len(m.Params) == 0 {
		return ""
	}
	return m.Params[len(m.Params)-1]
}
