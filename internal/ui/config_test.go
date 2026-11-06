package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func lastText(m *Model) string {
	lines := m.windows[m.active].lines
	return lines[len(lines)-1].text
}

func TestConfigRealname(t *testing.T) {
	m := New("tester", false)
	m.command("/config realname")
	if got := lastText(m); got != "realname is (the nickname)" {
		t.Fatalf("default: %q", got)
	}
	m.command(`/config realname "Ada Lovelace"`)
	if m.realname != "Ada Lovelace" || !strings.Contains(lastText(m), "next time you connect") {
		t.Fatalf("quoted value: %q, %q", m.realname, lastText(m))
	}
	m.command("/config realname")
	if got := lastText(m); got != "realname is Ada Lovelace" {
		t.Fatalf("show: %q", got)
	}
	m.command(`/config realname "unterminated`)
	if m.realname != "Ada Lovelace" || lastText(m) != "Missing closing quote." {
		t.Fatalf("unterminated quote changed state: %q, %q", m.realname, lastText(m))
	}
	m.command("/config realname \x1b[31mred")
	if m.realname != "Ada Lovelace" || !strings.HasPrefix(lastText(m), "Invalid realname") {
		t.Fatalf("control characters accepted: %q", m.realname)
	}
	m.command(`/config realname ""`)
	if m.realname != "" {
		t.Fatalf("empty quoted value did not reset: %q", m.realname)
	}
	m.command("/config colour red")
	if !strings.HasPrefix(lastText(m), "Usage:") {
		t.Fatalf("unknown key: %q", lastText(m))
	}
	if !slices.Contains(commandNames, "/config") {
		t.Fatal("/config is not tab completed")
	}
}

func TestConnectNeedsNickname(t *testing.T) {
	m := New("", false)
	if cmd := m.command("/connect irc.gloom.im"); cmd != nil || m.connecting {
		t.Fatal("connect started without a nickname")
	}
	if got := lastText(m); got != "Set a nickname first: /nick name" {
		t.Fatalf("got %q", got)
	}
	m.add(m.active, "bob", "hello there")
	if m.windows[m.active].mention {
		t.Fatal("empty nick matched every message")
	}
	m.command("/nick tester")
	if m.nick != "tester" || lastText(m) != "Nickname set to tester" {
		t.Fatalf("nick %q, message %q", m.nick, lastText(m))
	}
}

func TestConfigViewShowsNickAndRealname(t *testing.T) {
	m := New("tester", false)
	text := m.configText()
	if !strings.Contains(text, `realname  = "tester"`) {
		t.Fatalf("an unset realname should show the nickname:\n%s", text)
	}
	m.command("/nick ada")
	m.command(`/config realname "Ada Lovelace"`)
	text = m.configText()
	for _, want := range []string{`nick      = "ada"`, `realname  = "Ada Lovelace"`} {
		if !strings.Contains(text, want) {
			t.Errorf("config view lacks %s", want)
		}
	}
	m.toggleView(viewConfig)
	if !strings.Contains(ansi.Strip(m.View().Content), "Ada Lovelace") {
		t.Error("the rendered config view lacks the realname")
	}
}

func TestConfigViewDefaultsToGloomServerWithTLS(t *testing.T) {
	m := New("tester", false)
	text := m.configText()
	for _, want := range []string{`host      = "irc.gloom.im"`, `tls       = true`} {
		if !strings.Contains(text, want) {
			t.Errorf("config view lacks %s", want)
		}
	}
	m.conf.Host, m.conf.Port, m.conf.TLS = "irc2.gloom.im", "6667", false
	text = m.configText()
	for _, want := range []string{`host      = "irc2.gloom.im"`, `port      = 6667`, `tls       = false`} {
		if !strings.Contains(text, want) {
			t.Errorf("a configured server must show as set: lacks %s", want)
		}
	}
}
