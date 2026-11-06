package irc

import (
	"slices"
	"testing"
)

func TestParseMessage(t *testing.T) {
	tests := []struct {
		line, name, command, trailing string
		params                        []string
	}{
		{":friend!u@gloom.im PRIVMSG #gloom :hello there", "friend", "PRIVMSG", "hello there", []string{"#gloom", "hello there"}},
		{":irc.gloom.im 001 tester :Welcome", "irc.gloom.im", "001", "Welcome", []string{"tester", "Welcome"}},
		{"PING :probe", "", "PING", "probe", []string{"probe"}},
		{"PING", "", "PING", "", nil},
		{":a!u@gloom.im TOPIC #c :", "a", "TOPIC", "", []string{"#c", ""}},
		{":irc.gloom.im 353 me = #c :@a +b", "irc.gloom.im", "353", "@a +b", []string{"me", "=", "#c", "@a +b"}},
		{"@time=2026-01-01T00:00:00Z;x=y :a!u@gloom.im  join   #c\r\n", "a", "JOIN", "#c", []string{"#c"}},
		{":a!u@gloom.im PRIVMSG #c ::-)", "a", "PRIVMSG", ":-)", []string{"#c", ":-)"}},
	}
	for _, tc := range tests {
		m, err := ParseMessage(tc.line)
		if err != nil {
			t.Fatalf("%q: %v", tc.line, err)
		}
		if m.Name != tc.name || m.Command != tc.command || m.Trailing() != tc.trailing || !slices.Equal(m.Params, tc.params) {
			t.Fatalf("%q parsed as %+v", tc.line, m)
		}
	}
}

func TestParseMessageRejectsMalformed(t *testing.T) {
	for _, line := range []string{"", " ", ":", ":prefix", ":prefix ", "@tags", "@tags "} {
		if m, err := ParseMessage(line); err == nil {
			t.Fatalf("accepted %q as %+v", line, m)
		}
	}
}
