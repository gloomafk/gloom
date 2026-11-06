package ui

import (
	"fmt"
	"strings"
	"testing"
)

func lastLine(m *Model, i int) string {
	w := m.windows[i]
	if len(w.lines) == 0 {
		return ""
	}
	l := w.lines[len(w.lines)-1]
	return l.nick + " " + l.text
}

func TestIncomingMessagesOpenAtMostTenWindowsPerMinute(t *testing.T) {
	m := New("tester", false)
	for i := range maxAutoWindows {
		deliver(t, m, fmt.Sprintf(":n%d!u@gloom.im PRIVMSG tester :hi", i))
	}
	if got := len(m.windows); got != 1+maxAutoWindows {
		t.Fatalf("windows = %d, want %d", got, 1+maxAutoWindows)
	}

	deliver(t, m, ":spam!u@gloom.im PRIVMSG tester :buy now")
	if m.find("spam") >= 0 || len(m.windows) != 1+maxAutoWindows {
		t.Fatal("a window opened past the limit")
	}
	if got := lastLine(m, m.serverWindow()); got != "spam buy now" {
		t.Fatalf("server window shows %q, want the overflow message", got)
	}

	// A window that exists keeps receiving, and an unrelated one still cannot open.
	deliver(t, m, ":n0!u@gloom.im PRIVMSG tester :again")
	if got := lastLine(m, m.find("n0")); got != "n0 again" {
		t.Fatalf("existing window shows %q", got)
	}

	m.now = m.now.Add(autoWindowSpan)
	deliver(t, m, ":late!u@gloom.im PRIVMSG tester :hello")
	if m.find("late") < 0 {
		t.Fatal("the limit did not lift after a minute")
	}
}

func TestOverflowChannelMessageNamesItsChannel(t *testing.T) {
	m := New("tester", false)
	for i := range maxAutoWindows {
		deliver(t, m, fmt.Sprintf(":n%d!u@gloom.im PRIVMSG tester :hi", i))
	}
	deliver(t, m, ":bot!u@gloom.im PRIVMSG #ads :visit")
	if m.find("#ads") >= 0 {
		t.Fatal("a channel window opened past the limit")
	}
	if got := lastLine(m, m.serverWindow()); got != "bot #ads: visit" {
		t.Fatalf("server window shows %q", got)
	}
}

func TestServerMayJoinThreeChannelsPerConnection(t *testing.T) {
	m := New("tester", false)
	for i := range maxServerJoins {
		deliver(t, m, fmt.Sprintf(":tester!u@gloom.im JOIN #auto%d", i))
		if m.find(fmt.Sprintf("#auto%d", i)) < 0 {
			t.Fatalf("server join %d was refused", i)
		}
	}
	before := len(m.windows)
	deliver(t, m, ":tester!u@gloom.im JOIN #fourth")
	if m.find("#fourth") >= 0 || len(m.windows) != before {
		t.Fatal("a fourth server join opened a window")
	}
	if got := lastLine(m, m.serverWindow()); !strings.Contains(got, "Ignored a server join to #fourth") {
		t.Fatalf("server window shows %q, want a notice", got)
	}

	// Everything the server says about the refused channel stays out of the windows.
	for _, line := range []string{
		":irc.gloom.im 353 tester = #fourth :@alice tester",
		":irc.gloom.im 366 tester #fourth :End of NAMES",
		":irc.gloom.im 332 tester #fourth :topic",
		":irc.gloom.im 331 tester #fourth :none",
		":alice!u@gloom.im JOIN #fourth",
		":alice!u@gloom.im PART #fourth",
		":alice!u@gloom.im TOPIC #fourth :new",
		":alice!u@gloom.im MODE #fourth +o bob",
		":alice!u@gloom.im PRIVMSG #fourth :hello",
	} {
		deliver(t, m, line)
		if m.find("#fourth") >= 0 || len(m.windows) != before {
			t.Fatalf("%q opened a window for the refused channel", line)
		}
	}

	m.resetServerLimits()
	deliver(t, m, ":tester!u@gloom.im JOIN #fresh")
	if m.find("#fresh") < 0 {
		t.Fatal("a new connection did not get a fresh allowance")
	}
}

func TestUserJoinDoesNotCountAsServerJoin(t *testing.T) {
	m := New("tester", false)
	for i := range maxServerJoins + 2 {
		name := fmt.Sprintf("#mine%d", i)
		m.ensure(name) // /join opens the window before the server answers
		deliver(t, m, ":tester!u@gloom.im JOIN "+name)
		if !m.windows[m.find(name)].joined {
			t.Fatalf("%s was not joined", name)
		}
	}
	if m.serverJoins != 0 {
		t.Fatalf("serverJoins = %d, want 0", m.serverJoins)
	}
}
