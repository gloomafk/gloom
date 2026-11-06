package ui

import (
	"strings"
	"testing"
)

func joinModel(t *testing.T) *Model {
	t.Helper()
	conf, _ := localServer(t)
	m := New("tester", false)
	t.Cleanup(m.Close)
	v := m.connect(conf)().(connected)
	if v.err != nil {
		t.Fatal(v.err)
	}
	m.update(v)
	return m
}

func TestJoinWaitsForServerConfirmation(t *testing.T) {
	for _, entry := range []string{"command", "picker"} {
		t.Run(entry, func(t *testing.T) {
			m := joinModel(t)
			before, active := len(m.windows), m.active
			if entry == "command" {
				m.command("/join #gloom")
			} else {
				m.view = viewList
				m.list = []listEntry{{name: "#gloom"}}
				m.listSubmit("#gloom")
			}
			if len(m.windows) != before || m.active != active {
				t.Fatal("request opened or selected a channel before confirmation")
			}
			for _, line := range []string{
				":other!u@gloom.im JOIN :#gloom",
				":irc.gloom.im 332 tester #gloom :Channel topic",
				":irc.gloom.im 353 tester = #gloom :tester other",
				":other!u@gloom.im PRIVMSG #gloom :Hello",
			} {
				deliver(t, m, line)
				if m.find("#gloom") >= 0 || m.active != active {
					t.Fatalf("%q confirmed our pending join", line)
				}
			}
			deliver(t, m, ":tester!u@gloom.im JOIN :#GLOOM")
			if m.current().name != "#GLOOM" || !m.current().joined || m.view != viewWindow {
				t.Fatal("our join echo did not open and select the joined channel")
			}
			if len(m.pendingJoins) != 0 || m.serverJoins != 0 {
				t.Fatal("confirmed request retained pending state or consumed the server allowance")
			}
		})
	}
}

func TestJoinRefusalKeepsCurrentWindow(t *testing.T) {
	for _, code := range []string{"403", "405", "437", "471", "473", "474", "475", "476", "477", "489"} {
		t.Run(code, func(t *testing.T) {
			m := joinModel(t)
			before, active := len(m.windows), m.active
			m.command("/join #gloom")
			deliver(t, m, ":irc.gloom.im "+code+" tester #GLOOM :Cannot join channel")
			if len(m.windows) != before || m.active != active || len(m.pendingJoins) != 0 {
				t.Fatal("refusal opened a window, changed selection or retained the pending request")
			}
			if !strings.Contains(lastLine(m, m.serverWindow()), "Cannot join channel") {
				t.Fatal("join error was not shown in the server window")
			}
		})
	}
}

func TestJoinWithoutConnectionDoesNotOpenWindow(t *testing.T) {
	m := New("tester", false)
	m.joinChannel("#gloom")
	if len(m.windows) != 1 || len(m.pendingJoins) != 0 {
		t.Fatal("failed send opened a window or retained a pending join")
	}
}

func TestDisconnectClearsPendingJoins(t *testing.T) {
	m := joinModel(t)
	m.command("/join #gloom")
	m.disconnect()
	if len(m.pendingJoins) != 0 {
		t.Fatal("disconnect retained pending joins")
	}
}

func TestRequestedJoinOverridesPreviousServerRefusal(t *testing.T) {
	m := joinModel(t)
	m.serverJoins = maxServerJoins
	deliver(t, m, ":tester!u@gloom.im JOIN #gloom")
	m.command("/join #gloom")
	deliver(t, m, ":tester!u@gloom.im JOIN #gloom")
	if m.current().name != "#gloom" || !m.current().joined || m.refused["#gloom"] {
		t.Fatal("explicit join could not open a previously refused channel")
	}
}
