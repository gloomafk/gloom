package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func namesModel(t *testing.T) (*Model, func(...string) []string) {
	t.Helper()
	m, sent := closeModel(t)
	for _, channel := range []string{"#one", "#two"} {
		deliver(t, m, ":irc.gloom.im 366 tester "+channel+" :End of names")
	}
	m.selectWindow(m.find("#one"))
	check := func(want ...string) []string {
		t.Helper()
		// The sentinel proves there are no additional queued requests.
		m.command("/whois sentinel")
		lines := make([]string, len(want)+1)
		for i, line := range append(want, "WHOIS sentinel") {
			got := sent()
			lines[i] = got
			if before, ok := strings.CutSuffix(line, "*"); ok {
				if !strings.HasPrefix(got, before) {
					t.Fatalf("server received %q, want prefix %q", got, before)
				}
				continue
			}
			if got != line {
				t.Fatalf("server received %q, want %q", got, line)
			}
		}
		return lines
	}
	return m, check
}

func checkNamesFence(t *testing.T, check func(...string) []string) string {
	t.Helper()
	line := check("PING :names-*")[0]
	token, ok := strings.CutPrefix(line, "PING :")
	if !ok {
		t.Fatalf("server received %q, want a names fence PING", line)
	}
	return token
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
	for _, command := range []string{"/names", "/names #ONE", "/names #one irc.gloom.im", "/names "} {
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
				fence := checkNamesFence(t, check)
				if dirty {
					check()
				}
				deliver(t, m, ":irc.gloom.im PONG tester :"+fence)
				if dirty {
					check("NAMES #one")
				} else {
					check()
					deliver(t, m, ":alice!u@gloom.im MODE #one -v bob")
					check("NAMES #one")
				}
				before := len(w.lines)
				deliver(t, m, ":irc.gloom.im 353 tester = #one :tester bob")
				deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
				check()
				if len(w.lines) != before {
					t.Fatal("automatic reply became visible after the fence")
				}
				if len(w.nicks) != 2 || w.nicks["bob"] != "bob" {
					t.Fatalf("partial nicklist survived timeout: %v", w.nicks)
				}
				m.Update(tick(deadline.Add(namesTimeout)))
				check()
				m.Update(tick(deadline.Add(2 * namesTimeout)))
				check()
				if w.suppressNames || w.namesReceiving || len(w.namesRequests) != 0 {
					t.Fatal("timed-out reply state retained")
				}
				m.command("/names")
				check("NAMES #one")
				before = len(w.lines)
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
	deadline := m.now.Add(namesTimeout)
	m.Update(tick(deadline))
	fence := checkNamesFence(t, check)
	deliver(t, m, ":irc.gloom.im PONG tester :"+fence)
	check("NAMES #one")
	m.Update(tick(deadline.Add(namesTimeout)))
	checkNamesFence(t, check)
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
	fence := checkNamesFence(t, check)
	deliver(t, m, ":irc.gloom.im PONG tester :"+fence)
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
// Scenario: A late names reply cannot consume a newer request
//
//	Given an automatic names reply is stalled beyond its timeout
//	When its late end reply arrives before the fence reply
//	Then the late reply completes the automatic request
//	And the newer explicit reply remains visible
//
// ```
func TestNamesLateReplyBeforeFencePONG(t *testing.T) {
	m, check := namesModel(t)
	w := m.current()
	deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
	check("NAMES #one")
	deadline := m.now.Add(namesTimeout)
	m.Update(tick(deadline))
	fence := checkNamesFence(t, check)
	m.command("/names")
	check("NAMES #one")
	before := len(w.lines)
	deliver(t, m, ":irc.gloom.im 353 tester = #one :tester @alice")
	deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
	if len(w.lines) != before || len(w.namesRequests) != 1 || w.namesRequests[0].automatic {
		t.Fatal("late automatic reply consumed or exposed the newer explicit request")
	}
	deliver(t, m, ":irc.gloom.im PONG tester :"+fence)
	check()
	if len(w.namesRequests) != 1 || w.namesRequests[0].automatic {
		t.Fatal("fence removed a request sent after it")
	}
	before = len(w.lines)
	deliver(t, m, ":irc.gloom.im 353 tester = #one :tester @alice")
	deliver(t, m, ":irc.gloom.im 366 tester #one :End of names")
	check()
	if len(w.lines) != before+1 {
		t.Fatal("newer explicit reply was hidden")
	}
}

// ```gherkin
// Scenario: An unanswered fence can be replaced without accepting its late pong
//
//	Given a names fence receives no pong
//	When its timeout passes and a new fence is sent
//	Then a late pong for the first fence does not clear the second
//
// ```
func TestNamesFenceTimeout(t *testing.T) {
	m, check := namesModel(t)
	deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
	check("NAMES #one")
	start := m.now
	m.Update(tick(start.Add(namesTimeout)))
	firstFence := checkNamesFence(t, check)
	m.Update(tick(start.Add(2 * namesTimeout)))
	secondFence := checkNamesFence(t, check)
	deliver(t, m, ":irc.gloom.im PONG tester :"+firstFence)
	check()
	deliver(t, m, ":irc.gloom.im PONG tester :"+secondFence)
	check()
	deliver(t, m, ":alice!u@gloom.im MODE #one -v bob")
	check("NAMES #one")
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
			if len(w.namesRequests) != 0 || w.namesDirty || w.namesReceiving || w.suppressNames {
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
	if len(w.namesRequests) != 0 || w.suppressNames || w.namesDirty {
		t.Fatal("failed send retained an automatic request")
	}
	m.command("/names")
	if len(w.namesRequests) != 0 {
		t.Fatal("failed send retained an explicit request")
	}
}

// ```gherkin
// Scenario: Accept global end-of-names replies
//
//	Given an explicit global names request is pending
//	When the server sends one completion for the whole request
//	Then later automatic refreshes stay silent and finish normally
//	And no window opens for the aggregate reply target
//
// ```
func TestNamesAggregateCompletion(t *testing.T) {
	for _, target := range []string{"*"} {
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
// Scenario: Global completion preserves unrelated requests
//
//	Given two global requests and an automatic refresh overlap
//	And the first explicit reply may already have per-channel completions
//	When the first aggregate completion arrives
//	Then the second explicit reply stays visible
//	And the automatic refresh and its follow-up remain independent
//
// ```
func TestNamesAggregateOverlap(t *testing.T) {
	for _, target := range []string{"*"} {
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
				fence := checkNamesFence(t, check)
				deliver(t, m, ":irc.gloom.im PONG tester :"+fence)
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

// ```gherkin
// Scenario: A late global reply completes the older request
//
//	Given a global names reply is delayed beyond its timeout
//	When a newer global request is pending and the older reply arrives
//	Then the older reply does not complete the newer request
//	And the fence pong does not discard the newer request
//
// ```
func TestNamesGlobalBatchLateReplyBeforeFencePONG(t *testing.T) {
	m, check := namesModel(t)
	m.selectWindow(m.serverWindow())
	m.command("/names")
	check("NAMES")
	for _, channel := range []string{"#one", "#two"} {
		deliver(t, m, ":irc.gloom.im 366 tester "+channel+" :End of names")
	}
	check()
	if len(m.namesBatches) != 1 {
		t.Fatal("individual channel completions removed the global batch")
	}
	m.Update(tick(m.now.Add(namesTimeout)))
	fence := checkNamesFence(t, check)
	m.command("/names")
	check("NAMES")
	deliver(t, m, ":irc.gloom.im 366 tester * :End of names")
	check()
	if len(m.namesBatches) != 1 {
		t.Fatal("late global reply did not complete only the older batch")
	}
	deliver(t, m, ":irc.gloom.im PONG tester :"+fence)
	check()
	if len(m.namesBatches) != 1 {
		t.Fatal("fence removed the newer global batch")
	}
	for _, channel := range []string{"#one", "#two"} {
		w := m.windows[m.find(channel)]
		if len(w.namesRequests) != 1 || w.namesRequests[0].batch == nil {
			t.Fatalf("%s newer global request was lost", channel)
		}
	}
	deliver(t, m, ":irc.gloom.im 366 tester * :End of names")
	check()
	if len(m.namesBatches) != 0 {
		t.Fatal("newer global batch stayed pending")
	}
}

// ```gherkin
// Scenario: A lag pong does not clear a names fence
//
//	Given a lag ping and a names fence are both pending
//	When the server returns the lag pong
//	Then the names request remains pending until its matching fence pong
//
// ```
func TestNamesFenceAndLagPONGAreIndependent(t *testing.T) {
	m, check := namesModel(t)
	w := m.current()
	deliver(t, m, ":alice!u@gloom.im MODE #one +v bob")
	check("NAMES #one")
	m.registered = true
	m.Update(tick(m.now.Add(namesTimeout)))
	lines := check("PING :*", "PING :names-*")
	lagToken, ok := strings.CutPrefix(lines[0], "PING :")
	if !ok {
		t.Fatalf("lag ping %q has no token", lines[0])
	}
	fenceToken, ok := strings.CutPrefix(lines[1], "PING :")
	if !ok {
		t.Fatalf("names fence %q has no token", lines[1])
	}
	deliver(t, m, ":irc.gloom.im PONG tester :"+lagToken)
	check()
	if m.ping != "" || m.namesFence == nil || len(w.namesRequests) != 1 {
		t.Fatal("lag pong cleared the names fence")
	}
	deliver(t, m, ":irc.gloom.im PONG tester :"+fenceToken)
	check()
	if m.namesFence != nil || len(w.namesRequests) != 0 {
		t.Fatal("matching fence pong did not clear the names request")
	}
}

// ```gherkin
// Scenario: Split a multi-channel names request into independent requests
//
//	Given two joined channels
//	When the user requests names for both channels
//	Then the server receives one request per channel
//	And later automatic and explicit replies on the second channel are matched
//
// ```
func TestNamesMultiChannelSplit(t *testing.T) {
	m, check := namesModel(t)
	one, two := m.windows[m.find("#one")], m.windows[m.find("#two")]
	beforeWindows := len(m.windows)
	m.command("/names #one,#two")
	check("NAMES #one", "NAMES #two")
	for _, channel := range []string{"#one", "#two"} {
		before := len(m.windows[m.find(channel)].lines)
		deliver(t, m, ":irc.gloom.im 353 tester = "+channel+" :tester @alice")
		deliver(t, m, ":irc.gloom.im 366 tester "+channel+" :End of names")
		if got := len(m.windows[m.find(channel)].lines) - before; got != 1 {
			t.Fatalf("%s explicit reply added %d lines, want 1", channel, got)
		}
	}
	check()
	if len(one.namesRequests) != 0 || len(two.namesRequests) != 0 || len(m.namesBatches) != 0 {
		t.Fatal("multi-channel requests left stale tracking")
	}
	deliver(t, m, ":alice!u@gloom.im MODE #two +o carol")
	check("NAMES #two")
	before := len(two.lines)
	deliver(t, m, ":irc.gloom.im 353 tester = #two :tester @carol")
	deliver(t, m, ":irc.gloom.im 366 tester #two :End of names")
	check()
	if len(two.lines) != before {
		t.Fatal("automatic reply became visible after the multi-channel request")
	}
	m.selectWindow(m.find("#two"))
	m.command("/names")
	check("NAMES #two")
	before = len(two.lines)
	deliver(t, m, ":irc.gloom.im 353 tester = #two :tester @carol")
	deliver(t, m, ":irc.gloom.im 366 tester #two :End of names")
	check()
	if len(two.lines) != before+1 || len(m.windows) != beforeWindows {
		t.Fatal("later explicit reply was hidden or an aggregate window opened")
	}
}
