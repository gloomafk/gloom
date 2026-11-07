package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestDebugRowsFollowTrafficAndLayout(t *testing.T) {
	m := New("tester", false)
	for i := range maxRaw {
		m.logRaw(rawReceived, fmt.Sprintf("line %03d %s", i, strings.Repeat("text ", 20)))
	}
	for _, p := range []palette{darkPalette, lightPalette} {
		for _, width := range []int{100, 45, 100} {
			cached := m.rawRows(p, width)
			before := slices.Clone(cached)
			if !slices.Equal(before, rawRowsFor(m.raw, p, width)) {
				t.Fatal("debug rows did not follow the palette or width")
			}
			// Repeated renders must keep precisely the same styled content.
			if got := m.rawRows(p, width); !slices.Equal(got, before) || &got[0] != &cached[0] {
				t.Fatal("idle render changed or reformatted debug history")
			}
			// Appending at the history limit must replace the oldest line,
			// even though the number of protocol lines has not changed.
			m.logRaw(rawSent, "latest traffic")
			got := m.rawRows(p, width)
			if !slices.Equal(got, rawRowsFor(m.raw, p, width)) {
				t.Fatal("debug rows differ from freshly formatted history")
			}
			if slices.Equal(got, before) {
				t.Fatal("new traffic did not update debug history")
			}
			for _, row := range got {
				if ansi.StringWidth(row) != width {
					t.Fatalf("debug row width = %d, want %d", ansi.StringWidth(row), width)
				}
			}
		}
	}
}

func TestLinesThatShowNothingKeepRows(t *testing.T) {
	m := New("tester", false)
	deliver(t, m, ":tester!u@gloom.im JOIN #gloom")
	before := m.rowsVersion
	for _, raw := range []string{
		":irc.gloom.im 005 tester NETWORK=Example :are supported",
		":irc.gloom.im PONG irc.gloom.im :token",
		":irc.gloom.im 366 tester #gloom :End of /NAMES list.",
	} {
		deliver(t, m, raw)
	}
	if m.rowsVersion != before {
		t.Fatal("lines that add no row must not invalidate the cached rows")
	}
	for _, raw := range []string{
		":alice!u@gloom.im PRIVMSG #gloom :hello",
		":alice!u@gloom.im TOPIC #gloom :new topic",
	} {
		before = m.rowsVersion
		deliver(t, m, raw)
		if m.rowsVersion == before {
			t.Fatalf("%q must invalidate the cached rows", raw)
		}
	}
}

func TestNickListFollowsMembershipAndRank(t *testing.T) {
	m := New("tester", false)
	deliver(t, m, ":tester!u@gloom.im JOIN #gloom")
	deliver(t, m, ":irc.gloom.im 353 tester = #gloom :bob @alice +carol")
	w := m.windows[m.active]
	p := m.colors()
	order := func() string {
		rows, _ := m.nickRows(w, p, 10)
		var out []string
		for _, r := range rows[1:5] {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(ansi.Strip(r), "│")))
		}
		return strings.Join(out, ",")
	}
	if got := order(); got != "@ alice,+ carol,bob," {
		t.Fatalf("order = %q", got)
	}
	// A cached list must notice a rename, even though the count is unchanged.
	deliver(t, m, ":bob!u@gloom.im NICK zed")
	if got := order(); got != "@ alice,+ carol,zed," {
		t.Fatalf("after rename, order = %q", got)
	}
	// And a change made without going through receive.
	w.nicks["dave"] = "&dave"
	if got := order(); !strings.HasPrefix(got, "& dave,") {
		t.Fatalf("after direct edit, order = %q", got)
	}
}

func TestNickRowsDrawOnlyWhatFits(t *testing.T) {
	m := New("tester", false)
	w := m.windows[m.active]
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		w.nicks[n] = n
	}
	rows, hits := m.nickRows(w, m.colors(), 4)
	if len(rows) != 4 || len(hits) != 3 {
		t.Fatalf("rows=%d hits=%d, want 4 and 3", len(rows), len(hits))
	}
	if !strings.Contains(ansi.Strip(rows[0]), "6") {
		t.Fatal("header must still count every nick")
	}
}

func TestStyleIsShared(t *testing.T) {
	a := style("#112233", "#445566").Bold(true).Render("x")
	b := style("#112233", "#445566").Render("x")
	if a == b {
		t.Fatal("deriving a bold style must not change the cached one")
	}
	if got := style("#112233", "#445566").Render("x"); got != b {
		t.Fatal("cached style renders differently")
	}
}

func BenchmarkViewIdle(b *testing.B) {
	m := New("me", false)
	m.width, m.height = 120, 40
	i := m.ensure("#go")
	for n := range 300 {
		nick := "n" + strings.Repeat("x", n%7) + string(rune('a'+n%26)) + string(rune('a'+n/26%26))
		m.windows[i].nicks[strings.ToLower(nick)] = nick
	}
	for range 2000 {
		m.add(i, "user", "hello world this is a fairly long chat line with some more words to make it wrap around the pane")
	}
	m.selectWindow(i)
	m.View()
	for b.Loop() {
		m.View()
	}
}
