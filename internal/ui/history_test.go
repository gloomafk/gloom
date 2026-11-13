package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// ```gherkin
// Feature: Incremental conversation history
//
//	Scenario: Traffic in another conversation
//	  Given selected text in a visible conversation
//	  When another conversation receives messages and membership changes
//	  Then the visible history and selection stay unchanged
//	  And the other conversation records its activity
//
// ```
func TestHistoryIgnoresBackgroundTraffic(t *testing.T) {
	m := chatModel(t)
	w := m.current()
	other := m.ensure("#other")
	m.sel = selection{win: w, sig: m.selSig(), anchor: point{0, 0}, end: point{0, 80}, moved: true}
	selected := m.selectedText()
	rows := slices.Clone(m.chatRows.rows)
	first := &m.chatRows.rows[0]
	for _, event := range []string{
		":alice!u@gloom.im PRIVMSG #other :hello tester",
		":bob!u@gloom.im JOIN #other",
		":irc.gloom.im 353 tester = #other :@alice bob",
		":irc.gloom.im 366 tester #other :End of names",
	} {
		deliver(t, m, event)
		m.View()
		if &m.chatRows.rows[0] != first || !slices.Equal(m.chatRows.rows, rows) {
			t.Fatalf("background event reformatted visible history: %s", event)
		}
		if m.selectedText() != selected || m.sel.win != w {
			t.Fatal("background traffic changed the selection")
		}
	}
	if m.windows[other].unread == 0 || !m.windows[other].mention {
		t.Fatal("background traffic lost activity or mention state")
	}
}

func checkHistoryRows(t *testing.T, m *Model) {
	t.Helper()
	m.View()
	if m.current().raw {
		want := rawRowsFor(m.raw, m.colors(), m.body.w)
		if !slices.Equal(m.debugRows.rows, want) {
			t.Fatal("incremental debug history differs from a complete render")
		}
		if len(m.debugRows.ends) != len(m.raw) {
			t.Fatal("debug cache retained expired line boundaries")
		}
		return
	}
	want, hits := m.messageRows(m.current(), m.colors(), m.body.w)
	if !slices.Equal(m.chatRows.rows, want) || !slices.Equal(m.chatRows.hits, hits) {
		t.Fatal("incremental history or nickname targets differ from a complete render")
	}
	if len(m.chatRows.ends) != len(m.current().lines) {
		t.Fatal("chat cache retained expired line boundaries")
	}
}

// ```gherkin
// Feature: Bounded formatted history
//
//	Scenario: Single arrivals and bursts replace old history
//	  Given chat or debug history at its retention limit
//	  When wrapped messages arrive singly or replace the entire history
//	  Then all surviving rows match a complete render
//	  And expired rows and nickname targets are released
//
// ```
func TestHistoryAppendAndEviction(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%t", raw), func(t *testing.T) {
			m := chatModel(t)
			limit := maxLines
			if raw {
				m.toggleDebug()
				limit = maxRaw
			}
			add := func(n int) {
				text := fmt.Sprintf("message %d 界 e\u0301 %s", n, strings.Repeat("wrapped words ", n%7))
				if raw {
					m.logRaw(rawReceived, text)
					return
				}
				m.add(m.active, "alice", text)
			}
			for n := range limit {
				add(n)
			}
			checkHistoryRows(t, m)
			for _, count := range []int{1, 3, 17, limit, limit + 7} {
				for n := range count {
					add(n + limit)
				}
				checkHistoryRows(t, m)
			}
			for _, width := range []int{35, 140, 70} {
				m.Update(tea.WindowSizeMsg{Width: width, Height: 25})
				m.light = !m.light
				m.timestamps = !m.timestamps
				checkHistoryRows(t, m)
				add(width)
				checkHistoryRows(t, m)
			}
		})
	}
}

// ```gherkin
// Feature: Current styling in retained messages
//
//	Scenario: Membership changes without a displayed refresh reply
//	  Given messages from an operator in a joined channel
//	  When a hidden membership refresh, rename or departure changes that member
//	  Then retained messages use the current name and prefix
//	  And nickname clicks match the displayed content
//
// ```
func TestHistoryMembershipInvalidation(t *testing.T) {
	for name, event := range map[string]string{
		"hidden names": ":irc.gloom.im 353 tester = #gloom :+ALICE tester",
		"rename":       ":alice!u@gloom.im NICK renamed",
		"join":         ":alice!u@gloom.im JOIN #gloom",
		"part":         ":alice!u@gloom.im PART #gloom :bye",
		"kick":         ":tester!u@gloom.im KICK #gloom alice :bye",
		"self kick":    ":alice!u@gloom.im KICK #gloom tester :bye",
		"quit":         ":alice!u@gloom.im QUIT :bye",
	} {
		t.Run(name, func(t *testing.T) {
			m := chatModel(t)
			w := m.current()
			w.nicks["alice"] = "@alice"
			w.nicks["tester"] = "tester"
			w.rowsVersion++
			w.suppressJoinNames = true
			m.View()
			before := m.chatRows.rows[0]
			deliver(t, m, event)
			checkHistoryRows(t, m)
			if m.chatRows.rows[0] == before {
				t.Fatal("membership change left stale styling on the first message")
			}
		})
	}
}

// ```gherkin
// Feature: History lifecycle
//
//	Scenario: Clear and refill before the next display
//	  Given a cached conversation
//	  When it is cleared and refilled to the same message count
//	  Then only the new messages are displayed
//	Scenario: Close a cached conversation
//	  Given a displayed conversation
//	  When it is closed
//	  Then its formatted history is released
//
// ```
func TestHistoryClearAndClose(t *testing.T) {
	m := chatModel(t)
	for range 2 {
		m.command("/clear")
		for range 3 {
			m.add(m.active, "zed", "replacement history")
		}
		checkHistoryRows(t, m)
		if strings.Contains(strings.Join(m.chatRows.rows, ""), "first line") {
			t.Fatal("clear retained old formatted text")
		}
	}
	m.closeWindow(m.active)
	if m.chatRows.key.win != nil || len(m.chatRows.rows) != 0 || len(m.chatRows.hits) != 0 {
		t.Fatal("closing a conversation retained its formatted history")
	}
}

// ```gherkin
// Feature: Stable viewport during history eviction
//
//	Scenario: Wrapped arrivals while reading older chat or debug messages
//	  Given full history scrolled above the newest messages with a selection
//	  When wrapped messages arrive and the oldest messages expire
//	  Then surviving visible rows remain in place
//	  And the selection is cleared according to the retention policy
//
// ```
func TestHistoryEvictionPreservesViewport(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%t", raw), func(t *testing.T) {
			m := chatModel(t)
			limit := maxLines
			if raw {
				m.toggleDebug()
				limit = maxRaw
			}
			add := func(text string) {
				if raw {
					m.logRaw(rawReceived, text)
					return
				}
				m.add(m.active, "alice", text)
			}
			for n := range limit {
				add(fmt.Sprintf("old line %d", n))
			}
			m.scroll(10)
			m.View()
			before := slices.Clone(m.body.rows[m.body.start : m.body.start+m.body.h])
			m.sel = selection{win: m.current(), sig: m.selSig(), moved: true}
			add(strings.Repeat("new wrapped text ", 40))
			checkHistoryRows(t, m)
			if !slices.Equal(before, m.body.rows[m.body.start:m.body.start+m.body.h]) {
				t.Fatal("arrival moved surviving visible rows")
			}
			if m.sel.win != nil {
				t.Fatal("eviction kept a selection with obsolete row indexes")
			}
		})
	}
}

// ```gherkin
// Feature: Reuse formatted content
//
//	Scenario: An arrival in a long conversation
//	  Given a warm conversation with a full history
//	  When a message arrives
//	  Then formatting allocations remain comparable to a short conversation
//
// ```
func TestHistoryArrivalAllocationsStayBounded(t *testing.T) {
	allocs := func(history int) float64 {
		m := chatModel(t)
		for range history {
			m.add(m.active, "alice", "message with wrapped words 界 e\u0301")
		}
		m.View()
		return testing.AllocsPerRun(10, func() {
			m.add(m.active, "alice", "another message with wrapped words 界 e\u0301")
			m.View()
		})
	}
	short, full := allocs(20), allocs(maxLines)
	if full > short*2 {
		t.Fatalf("arrival allocations scale with history: short=%.0f full=%.0f", short, full)
	}
}

// ```gherkin
// Feature: Release expired formatted text
//
//	Scenario: History evicts a wrapped message
//	  Given formatted rows for several retained messages
//	  When the oldest message expires
//	  Then its text is no longer held in the retained backing storage
//
// ```
func TestHistoryTrimReleasesExpiredRows(t *testing.T) {
	rows := []string{"expired", "continuation", "retained"}
	c := rowCache{rows: rows, ends: []int{2, 3}}
	if removed := c.trim(1); removed != 2 || rows[0] != "" || rows[1] != "" {
		t.Fatal("expired rows remain referenced")
	}
	if !slices.Equal(c.rows, []string{"retained"}) || !slices.Equal(c.ends, []int{1}) {
		t.Fatal("surviving rows have incorrect boundaries")
	}
	c.trim(2)
	if c.rows != nil || c.ends != nil {
		t.Fatal("completely expired history retains backing storage")
	}
}

// ```gherkin
// Feature: Welcome history rendering
//
//	Scenario: Append after the welcome animation freezes
//	  Given a visible welcome animation
//	  When typing freezes it and messages arrive
//	  Then the image stays in its original position before the new messages
//
// ```
func TestHistoryAppendPreservesWelcome(t *testing.T) {
	m := New("tester", false)
	m.width, m.height = 100, 40
	m.View()
	m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	for range 3 {
		m.note(0, "after welcome")
		checkHistoryRows(t, m)
	}
	if !strings.Contains(ansi.Strip(m.chatRows.rows[len(m.chatRows.rows)-1]), "after welcome") {
		t.Fatal("welcome image moved after the new messages")
	}
}
