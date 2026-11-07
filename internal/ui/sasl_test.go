package ui

import (
	"testing"

	"gloom.im/gloom/internal/irc"
)

func serverText(m *Model) string {
	lines := m.srv.lines
	return lines[len(lines)-1].text
}

func TestSASLNoticeAndLoginShowInServerWindow(t *testing.T) {
	m := sized(120, 24)
	t.Cleanup(m.Close)
	m.connect(irc.Config{Host: "irc.gloom.im", Port: "6697", Nick: "tester", TLS: true})
	m.update(incoming{event: irc.Event{Notice: "SASL authentication succeeded."}, ok: true})
	if got := serverText(m); got != "SASL authentication succeeded." {
		t.Fatalf("notice = %q", got)
	}
	deliver(t, m, ":irc.gloom.im 900 tester tester!tester@gloom.im account :You are now logged in as account")
	if got := serverText(m); got != "You are now logged in as account" {
		t.Fatalf("900 = %q", got)
	}
}

func TestConnectReportsSASLErrors(t *testing.T) {
	for input, want := range map[string]string{
		"/connect -sasl-user account -sasl-pass secret irc.gloom.im": "SASL sends the password, so it needs TLS: add -tls",
		"/connect -tls -sasl-user account irc.gloom.im":              "SASL needs both an account and a password",
		"/connect -tls -sasl-pass":                                   irc.ConnectUsage,
	} {
		m := New("tester", false)
		m.command(input)
		if got := lastText(m); got != want {
			t.Errorf("%s: %q, want %q", input, got, want)
		}
		if m.connecting {
			t.Errorf("%s: started connecting", input)
		}
	}
}

func TestModelDropsSASLPasswordOnceConnected(t *testing.T) {
	conf, _ := localServer(t)
	m := New("tester", false)
	t.Cleanup(m.Close)
	result := m.connect(conf)().(connected)
	// Update also drops credentials from a result constructed by a caller.
	result.config.SASL = irc.SASL{Mechanism: "PLAIN", User: "account", Pass: "secret"}
	m.update(result)
	if m.client == nil {
		t.Fatalf("not connected: %v", result.err)
	}
	if m.conf.SASL != (irc.SASL{}) || m.conf.Host != conf.Host {
		t.Fatalf("model kept %+v", m.conf)
	}
}

func TestStartConfigDropsSASLPasswordAfterInit(t *testing.T) {
	m := New("tester", false)
	t.Cleanup(m.Close)
	m.Start = &irc.Config{Host: "irc.gloom.im", Port: "6697", Nick: "tester", TLS: true, SASL: irc.SASL{Mechanism: "PLAIN", User: "account", Pass: "secret"}}
	m.Init()
	if m.Start.SASL != (irc.SASL{}) {
		t.Fatalf("start config kept %+v", m.Start.SASL)
	}
}

func TestConnectCredentialsStayOutOfHistory(t *testing.T) {
	for _, verb := range []string{"/connect", "/SERVER", "/connect\t"} {
		for _, debug := range []bool{false, true} {
			m := sized(120, 24)
			t.Cleanup(m.Close)
			if debug {
				m.toggleDebug()
			}
			m.input.SetValue(verb + " -sasl-user account -sasl-pass secret irc.gloom.im")
			m.submit()
			if len(m.history) != 0 || m.input.Value() != "" || m.draft != "" {
				t.Fatal("connection command retained in input history")
			}
		}
	}
}
