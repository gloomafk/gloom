package ui

import (
	"slices"
	"strings"
	"testing"
)

func TestPlainTextInStatusWindowIsAnUnknownCommand(t *testing.T) {
	m := New("tester", false)
	for in, want := range map[string]string{
		"asdf":       "Unknown command: asdf. Type /help.",
		"asdf hello": "Unknown command: asdf hello. Type /help.",
	} {
		m.input.SetValue(in)
		m.submit()
		if got := lastText(m); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestMeIsUnknownAndAbsentFromHelpAndCompletion(t *testing.T) {
	m := New("tester", false)
	m.selectWindow(m.ensure("#gloom"))
	m.command("/me waves")
	if got := lastText(m); got != "Unknown command: /me. Type /help." {
		t.Fatalf("got %q, want unknown command", got)
	}
	if slices.Contains(commandNames, "/me") {
		t.Fatal("action command remains in completion")
	}
	m.command("/help")
	for _, line := range m.current().lines {
		if strings.Contains(line.text, "/me action") {
			t.Fatal("action command remains in help")
		}
	}
}
