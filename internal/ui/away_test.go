package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"gloom.im/gloom/internal/irc"
)

// awayModel is a model registered on irc.gloom.im with the server window shown.
func awayModel(t *testing.T) *Model {
	t.Helper()
	m := sized(120, 24)
	t.Cleanup(m.Close)
	m.connect(irc.Config{Host: "irc.gloom.im", Port: "6667", Nick: "tester"})
	deliver(t, m, ":irc.gloom.im 001 tester :Welcome")
	m.selectWindow(m.serverWindow())
	return m
}

// title is the first rendered row, the active window's topic bar.
func title(m *Model) string {
	row, _, _ := strings.Cut(ansi.Strip(m.View().Content), "\n")
	return row
}

func TestAwayRepliesMarkServerWindowTitle(t *testing.T) {
	m := awayModel(t)
	if strings.Contains(title(m), "(zZzZ)") {
		t.Fatal("title marked away before the server confirmed it")
	}

	deliver(t, m, ":irc.gloom.im 306 tester :You have been marked as being away")
	if got := title(m); !strings.Contains(got, "connected as tester (zZzZ)") {
		t.Fatalf("away title missing marker: %q", got)
	}
	lines := m.srv.lines
	if got := lines[len(lines)-1].text; got != "You have been marked as being away" {
		t.Fatalf("306 reply not shown in the server window: %q", got)
	}

	m.selectWindow(m.find("#gloom"))
	if got := title(m); strings.Contains(got, "(zZzZ)") {
		t.Fatalf("channel title must not carry the away marker: %q", got)
	}

	m.selectWindow(m.serverWindow())
	deliver(t, m, ":irc.gloom.im 305 tester :You are no longer marked as being away")
	if got := title(m); strings.Contains(got, "(zZzZ)") {
		t.Fatalf("title still marked after 305: %q", got)
	}
}

func TestDisconnectClearsAway(t *testing.T) {
	m := awayModel(t)
	deliver(t, m, ":irc.gloom.im 306 tester :You have been marked as being away")
	m.disconnect()
	if got := title(m); m.away || strings.Contains(got, "(zZzZ)") {
		t.Fatalf("away status survived a disconnect: %q", got)
	}
}
