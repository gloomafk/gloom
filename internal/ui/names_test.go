package ui

import (
	"fmt"
	"testing"
	"time"
)

func namesModel(t *testing.T) (*Model, func(...string)) {
	t.Helper()
	m, sent := closeModel(t)
	for _, channel := range []string{"#one", "#two"} {
		deliver(t, m, ":irc.gloom.im 366 tester "+channel+" :End of names")
	}
	m.selectWindow(m.find("#one"))
	check := func(want ...string) {
		t.Helper()
		// The sentinel proves there are no additional queued requests.
		m.command("/whois sentinel")
		for _, line := range append(want, "WHOIS sentinel") {
			if got := sent(); got != line {
				t.Fatalf("server received %q, want %q", got, line)
			}
		}
	}
	return m, check
}

// ```gherkin
// Scenario: Coalesce membership modes until each automatic reply ends
//
//	Given two joined channels with complete nicklists
//	When twenty membership modes arrive for one channel before a names reply
//	Then only one refresh and one follow-up are sent for that channel
//	And the other channel can refresh independently
//	And every automatic reply remains hidden from history
//
// ```
func TestNamesCoalesceMembershipModes(t *testing.T) {
	m, check := namesModel(t)
	w := m.current()
	for range 20 {
		deliver(t, m, ":alice!u@gloom.im MODE #ONE +v bob")
	}
	check("NAMES #one")
	deliver(t, m, ":alice!u@gloom.im MODE #two +o carol")
	check("NAMES #two")
	before := len(w.lines)
	deliver(t, m, ":irc.gloom.im 353 tester = #one :tester @alice")
	deliver(t, m, ":irc.gloom.im 353 tester = #one :bob")
	check()
	deliver(t, m, ":irc.gloom.im 366 tester #ONE :End of names")
	check("NAMES #one")
	deliver(t, m, ":irc.gloom.im 353 tester = #one :tester @alice +bob")
	deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
	deliver(t, m, ":irc.gloom.im 366 tester #two :End of names")
	check()
	if len(w.lines) != before || len(w.nicks) != 3 || w.nicks["bob"] != "+bob" {
		t.Fatalf("unexpected automatic reply: lines=%d nicks=%v", len(w.lines)-before, w.nicks)
	}
	deliver(t, m, ":alice!u@gloom.im MODE #one -v bob")
	check("NAMES #one")
	// A non-membership mode must not mark an in-flight refresh dirty.
	deliver(t, m, ":irc.gloom.im MODE #one +nt")
	deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
	check()
}

// ```gherkin
// Scenario: Coalesce changes during the initial join reply
//
//	Given the server has not completed the automatic names reply on join
//	When membership modes change repeatedly
//	Then no NAMES is sent until the join reply ends
//	And exactly one refresh follows it
//
// ```
func TestNamesCoalesceDuringJoin(t *testing.T) {
	m, check := namesModel(t)
	deliver(t, m, ":tester!u@gloom.im JOIN #one")
	for range 20 {
		deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
	}
	check()
	deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
	check("NAMES #one")
	deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
	check()
}

// ```gherkin
// Scenario: Coalesce changes during the follow-up too
//
//	Given a follow-up names refresh is pending
//	When more membership modes arrive before its reply ends
//	Then they produce only one further refresh
//
// ```
func TestNamesChangesDuringFollowUp(t *testing.T) {
	m, check := namesModel(t)
	for range 3 {
		for range 20 {
			deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
		}
		check("NAMES #one")
		deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
	}
	check("NAMES #one")
	deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
	check()
}

// ```gherkin
// Scenario: Preserve explicit requests mixed with automatic refreshes
//
//	Given automatic and explicit names requests overlap on a channel
//	When their replies arrive in request order
//	Then every explicit request is sent and its replies are visible
//	And automatic replies stay hidden and coalesce subsequent mode changes
//
// ```
func TestNamesExplicitOverlap(t *testing.T) {
	for _, command := range []string{"/names", "/names #ONE", "/names #one,#two", "/names #one irc.gloom.im", "/names "} {
		for _, explicitFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicitFirst=%t", command, explicitFirst), func(t *testing.T) {
				m, check := namesModel(t)
				w := m.current()
				if command == "/names " {
					m.selectWindow(m.serverWindow())
				}
				wire := map[string]string{
					"/names":                   "NAMES #one",
					"/names #ONE":              "NAMES #ONE",
					"/names #one,#two":         "NAMES #one,#two",
					"/names #one irc.gloom.im": "NAMES #one irc.gloom.im",
					"/names ":                  "NAMES",
				}[command]
				mode := func() { deliver(t, m, ":alice!u@gloom.im MODE #one +v bob") }
				explicit := func() {
					m.command(command)
					m.command(command)
					check(wire, wire)
				}
				if explicitFirst {
					explicit()
				}
				mode()
				check("NAMES #one")
				if !explicitFirst {
					explicit()
				}
				mode()
				check()
				reply := func(visible bool) {
					before := len(w.lines)
					deliver(t, m, ":irc.gloom.im 353 tester = #one :tester @alice")
					deliver(t, m, ":irc.gloom.im 353 tester = #one :+bob")
					deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
					want := 0
					if visible {
						want = 2
					}
					if got := len(w.lines) - before; got != want {
						t.Fatalf("reply added %d lines, want %d", got, want)
					}
				}
				if explicitFirst {
					reply(true)
					reply(true)
					check()
				}
				reply(false)
				check("NAMES #one")
				if !explicitFirst {
					reply(true)
					reply(true)
					check()
				}
				reply(false)
				check()
			})
		}
	}
}

// ```gherkin
// Scenario: Recover after a missing end-of-names reply
//
//	Given an automatic names reply is missing or incomplete
//	When its thirty-second deadline passes
//	Then pending mode changes cause one follow-up with a fresh nicklist
//	And otherwise the next mode change can start a refresh
//	And no endless retries are sent
//
// ```
func TestNamesTimeout(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, dirty := range []bool{false, true} {
			t.Run(fmt.Sprintf("partial=%t/dirty=%t", partial, dirty), func(t *testing.T) {
				m, check := namesModel(t)
				w := m.current()
				deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
				check("NAMES #one")
				if partial {
					deliver(t, m, ":irc.gloom.im 353 tester = #one :stale")
				}
				if dirty {
					deliver(t, m, ":alice!u@gloom.im MODE #one -v bob")
				}
				deadline := m.now.Add(namesTimeout)
				m.Update(tick(deadline.Add(-time.Nanosecond)))
				check()
				m.Update(tick(deadline))
				if !dirty {
					check()
					deliver(t, m, ":alice!u@gloom.im MODE #one -v bob")
				}
				check("NAMES #one")
				deliver(t, m, ":irc.gloom.im 353 tester = #one :tester bob")
				if len(w.nicks) != 2 || w.nicks["bob"] != "bob" {
					t.Fatalf("partial nicklist survived timeout: %v", w.nicks)
				}
				m.Update(tick(deadline.Add(namesTimeout)))
				check()
				m.Update(tick(deadline.Add(2 * namesTimeout)))
				check()
				if w.suppressJoinNames || w.namesReceiving || len(w.namesRequests) != 0 {
					t.Fatal("timed-out reply state retained")
				}
				m.command("/names")
				check("NAMES #one")
				before := len(w.lines)
				deliver(t, m, ":irc.gloom.im 353 tester = #one :tester bob")
				if len(w.lines) != before+1 {
					t.Fatal("explicit reply hidden after timeout")
				}
			})
		}
	}
}

// ```gherkin
// Scenario: Recover when a join names reply never arrives
//
//	Given the join names reply is missing and membership modes changed
//	When thirty seconds pass
//	Then one refresh is sent and no further retry follows without new changes
//
// ```
func TestNamesJoinTimeout(t *testing.T) {
	m, check := namesModel(t)
	deliver(t, m, ":tester!u@gloom.im JOIN #one")
	deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
	check()
	m.Update(tick(m.now.Add(namesTimeout)))
	check("NAMES #one")
	m.Update(tick(m.now.Add(namesTimeout)))
	check()
}

// ```gherkin
// Scenario: Expiring an explicit request does not finish a newer refresh
//
//	Given an explicit names reply is missing
//	And a newer automatic refresh has subsequent mode changes pending
//	When the explicit request reaches its timeout
//	Then no automatic follow-up is sent before the newer refresh ends
//
// ```
func TestNamesExplicitTimeoutBeforeAutomatic(t *testing.T) {
	m, check := namesModel(t)
	m.command("/names")
	check("NAMES #one")
	m.Update(tick(m.now.Add(time.Second)))
	for range 2 {
		deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
	}
	check("NAMES #one")
	m.Update(tick(m.now.Add(namesTimeout - time.Second)))
	check()
	w := m.current()
	before := len(w.lines)
	deliver(t, m, ":irc.gloom.im 353 tester = #one :tester +bob")
	deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
	check("NAMES #one")
	if len(w.lines) != before {
		t.Fatal("automatic reply became visible after explicit timeout")
	}
}

// ```gherkin
// Scenario: Clear pending refreshes when channel membership ends
//
//	Given a channel has an automatic refresh and a follow-up pending
//	When the user parts, closes the window, is kicked, or disconnects
//	Then no follow-up is sent
//	And rejoining starts fresh names reply state
//
// ```
func TestNamesDeparture(t *testing.T) {
	for _, action := range []string{"part", "close", "kick", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			m, check := namesModel(t)
			for range 2 {
				deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
			}
			check("NAMES #one")
			switch action {
			case "part":
				deliver(t, m, ":tester!u@gloom.im PART #one")
			case "close":
				m.command("/close")
				check("PART #one")
			case "kick":
				deliver(t, m, ":alice!u@gloom.im KICK #one tester :bye")
			case "disconnect":
				m.disconnect()
			}
			m.Update(tick(m.now.Add(namesTimeout)))
			if action != "disconnect" {
				check()
			}
			deliver(t, m, ":tester!u@gloom.im JOIN #one")
			w := m.current()
			deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
			if len(w.namesRequests) != 0 || w.namesDirty || w.namesReceiving || w.suppressJoinNames {
				t.Fatal("old names state survived rejoin")
			}
			if action != "disconnect" {
				check()
			}
		})
	}
}

// ```gherkin
// Scenario: A failed send does not leave a refresh in flight
//
//	Given the connection cannot accept a NAMES command
//	When a membership mode triggers a refresh
//	Then no pending reply or suppression state is retained
//
// ```
func TestNamesSendFailure(t *testing.T) {
	m, _ := namesModel(t)
	m.client.Close()
	deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
	w := m.current()
	if len(w.namesRequests) != 0 || w.suppressJoinNames || w.namesDirty {
		t.Fatal("failed send retained an automatic request")
	}
	m.command("/names")
	if len(w.namesRequests) != 0 {
		t.Fatal("failed send retained an explicit request")
	}
}

// ```gherkin
// Scenario: Accept global and multi-channel end-of-names replies
//
//	Given an explicit global or multi-channel names request is pending
//	When the server sends one completion for the whole request
//	Then later automatic refreshes stay silent and finish normally
//	And no window opens for the aggregate reply target
//
// ```
func TestNamesAggregateCompletion(t *testing.T) {
	for _, target := range []string{"*", "#ONE,#two"} {
		t.Run(target, func(t *testing.T) {
			m, check := namesModel(t)
			beforeWindows := len(m.windows)
			if target == "*" {
				m.selectWindow(m.serverWindow())
				m.command("/names")
				check("NAMES")
			} else {
				m.command("/names " + target)
				check("NAMES " + target)
			}
			deliver(t, m, ":irc.gloom.im 366 tester "+target+" :End of names")
			for _, channel := range []string{"#one", "#two"} {
				w := m.windows[m.find(channel)]
				deliver(t, m, ":alice!u@gloom.im MODE "+channel+" +v bob")
				check("NAMES " + channel)
				before := len(w.lines)
				deliver(t, m, ":irc.gloom.im 353 tester = "+channel+" :tester +bob")
				deliver(t, m, ":irc.gloom.im 366 tester "+channel+" :End of names")
				check()
				if len(w.lines) != before || len(w.namesRequests) != 0 {
					t.Fatalf("%s: automatic reply became visible or stayed pending", channel)
				}
			}
			if len(m.windows) != beforeWindows || len(m.namesBatches) != 0 {
				t.Fatal("aggregate completion opened a window or retained a batch")
			}
		})
	}
}

// ```gherkin
// Scenario: Aggregate completion preserves unrelated requests
//
//	Given two global or multi-channel requests and an automatic refresh overlap
//	And the first explicit reply may already have per-channel completions
//	When the first aggregate completion arrives
//	Then the second explicit reply stays visible
//	And the automatic refresh and its follow-up remain independent
//
// ```
func TestNamesAggregateOverlap(t *testing.T) {
	for _, target := range []string{"*", "#one,#two"} {
		for _, individualEnds := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/individualEnds=%t", target, individualEnds), func(t *testing.T) {
				m, check := namesModel(t)
				one := m.windows[m.find("#one")]
				two := m.windows[m.find("#two")]
				// An unrelated automatic request comes before both explicit requests.
				deliver(t, m, ":alice!u@gloom.im MODE #two +v bob")
				check("NAMES #two")
				command, wire := "/names "+target, "NAMES "+target
				if target == "*" {
					m.selectWindow(m.serverWindow())
					command, wire = "/names", "NAMES"
				}
				for range 2 {
					m.command(command)
					check(wire)
				}
				// Another automatic request comes after both explicit requests.
				for range 2 {
					deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
				}
				check("NAMES #one")
				if individualEnds {
					deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
				}
				deliver(t, m, ":irc.gloom.im 366 tester "+target+" :End of names")
				check()
				if len(two.namesRequests) != 2 || !two.namesRequests[0].automatic {
					t.Fatal("aggregate completion consumed the preceding automatic request")
				}
				before := len(one.lines)
				deliver(t, m, ":irc.gloom.im 353 tester = #one :tester +bob")
				if len(one.lines) != before+1 {
					t.Fatal("second explicit request was completed by the first aggregate reply")
				}
				deliver(t, m, ":irc.gloom.im 366 tester "+target+" :End of names")
				check()
				before = len(one.lines)
				deliver(t, m, ":irc.gloom.im 353 tester = #one :tester +bob")
				deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
				check("NAMES #one")
				if len(one.lines) != before {
					t.Fatal("automatic reply was displayed after aggregate completion")
				}
				// A stray aggregate reply must not finish the automatic follow-up.
				deliver(t, m, ":irc.gloom.im 366 tester "+target+" :End of names")
				if len(one.namesRequests) != 1 || !one.namesRequests[0].automatic {
					t.Fatal("unmatched aggregate reply consumed an automatic request")
				}
			})
		}
	}
}

// ```gherkin
// Scenario: Release aggregate tracking after a missing reply or disconnect
//
//	Given an explicit global names request has no reply
//	When it times out or the user disconnects
//	Then its reply tracking is cleared
//
// ```
func TestNamesAggregateCleanup(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnect=%t", disconnect), func(t *testing.T) {
			m, check := namesModel(t)
			m.selectWindow(m.serverWindow())
			m.command("/names")
			check("NAMES")
			if disconnect {
				m.disconnect()
			} else {
				m.Update(tick(m.now.Add(namesTimeout)))
				check()
			}
			if len(m.namesBatches) != 0 {
				t.Fatal("aggregate tracking survived cleanup")
			}
			for _, w := range m.windows {
				if len(w.namesRequests) != 0 {
					t.Fatal("channel tracking survived cleanup")
				}
			}
		})
	}
}
