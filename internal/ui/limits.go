package ui

import (
	"slices"
	"time"

	"gloom.im/gloom/internal/irc"
)

// Limits on windows that the server opens without the user asking, so a
// spammer or a hostile server cannot fill the status bar.
const (
	// maxAutoWindows is how many windows incoming messages may open within
	// autoWindowSpan. Messages past it are shown in the server window.
	maxAutoWindows = 10
	autoWindowSpan = 60 * time.Second
	// maxServerJoins is how many channels the server may join the user to,
	// without a /join, over the life of one connection.
	maxServerJoins = 3
)

// allowAutoWindow reports whether an incoming message may open one more
// window, and counts it when it may. It slides: an open stops counting once
// autoWindowSpan has passed.
func (m *Model) allowAutoWindow() bool {
	m.autoOpens = slices.DeleteFunc(m.autoOpens, func(t time.Time) bool { return m.now.Sub(t) >= autoWindowSpan })
	if len(m.autoOpens) >= maxAutoWindows {
		return false
	}
	m.autoOpens = append(m.autoOpens, m.now)
	return true
}

func (m *Model) allowServerJoin() bool {
	if m.serverJoins >= maxServerJoins {
		return false
	}
	m.serverJoins++
	return true
}

func (m *Model) resetServerLimits() {
	m.serverJoins = 0
	m.refused = nil
}

// refuse remembers a channel whose server join was refused, so the replies
// that follow it (names, topic, modes) do not open its window either.
func (m *Model) refuse(channel string) {
	if m.refused == nil {
		m.refused = map[string]bool{}
	}
	m.refused[irc.Fold(irc.Clean(channel))] = true
}

// channelWindow returns the window for a channel named by the server. It
// reports false when there is none and the channel was refused.
func (m *Model) channelWindow(name string) (int, bool) {
	clean := irc.Clean(name)
	if i := m.find(clean); i >= 0 {
		return i, true
	}
	if m.refused[irc.Fold(clean)] {
		return -1, false
	}
	return m.ensure(clean), true
}
