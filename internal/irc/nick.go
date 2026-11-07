package irc

import (
	"errors"
	"fmt"
)

func validNicknames(nicks [3]string) bool {
	if !ValidNick(nicks[0]) {
		return false
	}
	empty := false
	for _, nick := range nicks[1:] {
		if nick == "" {
			empty = true
			continue
		}
		if empty || !ValidNick(nick) {
			return false
		}
	}
	return true
}

// SetNicknames replaces the ordered preferences and requests the first nick.
// Registration retries use the remaining choices until numeric 001 arrives.
func (c *Client) SetNicknames(nicks [3]string) error {
	if !validNicknames(nicks) {
		return errors.New("invalid nickname list")
	}
	c.nickMu.Lock()
	defer c.nickMu.Unlock()
	if err := c.Send("NICK " + nicks[0]); err != nil {
		return err
	}
	c.nicknames, c.nickIndex = nicks, 0
	if !c.registered {
		c.nick = nicks[0]
	}
	return nil
}

func (c *Client) handleNick(m *Message) (string, error) {
	c.nickMu.Lock()
	defer c.nickMu.Unlock()
	switch m.Command {
	case "001":
		c.registered = true
		if len(m.Params) > 0 {
			c.nick = Clean(m.Params[0])
		}
	case "NICK":
		if Fold(m.Name) == Fold(c.nick) && len(m.Params) > 0 {
			c.nick = Clean(m.Params[0])
		}
	case "432", "433":
		if c.registered || len(m.Params) < 2 || Fold(m.Params[1]) != Fold(c.nick) {
			return "", nil
		}
		next := c.nickIndex + 1
		if next >= len(c.nicknames) || c.nicknames[next] == "" {
			return "", nil
		}
		nick := c.nicknames[next]
		if err := c.Send("NICK " + nick); err != nil {
			return "", err
		}
		reason := "is in use"
		if m.Command == "432" {
			reason = "is invalid"
		}
		notice := fmt.Sprintf("Nickname %s %s, trying %s", c.nick, reason, nick)
		c.nick, c.nickIndex = nick, next
		return notice, nil
	}
	return "", nil
}
