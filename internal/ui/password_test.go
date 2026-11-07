package ui

import (
	"fmt"
	"gloom.im/gloom/internal/irc"
	"testing"
)

func TestPasswordSubmitFromEveryView(t *testing.T) {
	for _, verb := range []string{"/connect", "/SERVER"} {
		for _, debug := range []bool{false, true} {
			t.Run(fmt.Sprint(verb, debug), func(t *testing.T) {
				conf, _ := localServer(t)
				m := New("tester", false)
				t.Cleanup(m.Close)
				if debug {
					m.toggleDebug()
				}
				m.input.SetValue(fmt.Sprintf("%s -pass example-password %s %s", verb, conf.Host, conf.Port))
				cmd := m.submit()
				if cmd == nil {
					t.Fatal("connection command was not handled locally")
				}
				if len(m.history) != 0 || m.draft != "" || m.input.Value() != "" {
					t.Fatal("connection input retained")
				}
				result := cmd().(connected)
				if result.err != nil {
					t.Fatal(result.err)
				}
				if result.config.Password != "" {
					t.Fatal("connection result retained password")
				}
				m.Update(result)
				if m.conf.Password != "" {
					t.Fatal("model retained password")
				}
			})
		}
	}
}

func TestInvalidPasswordCommandStaysPrivate(t *testing.T) {
	for _, input := range []string{"/connect -pass", "/server -pass", "/connect -pass example\npassword irc.gloom.im"} {
		m := New("tester", false)
		m.input.SetValue(input)
		if cmd := m.submit(); cmd != nil || m.connecting {
			t.Fatal("invalid connection started")
		}
		if len(m.history) != 0 {
			t.Fatal("invalid command retained in history")
		}
	}
}

func TestStartConfigDropsServerPassword(t *testing.T) {
	m := New("tester", false)
	t.Cleanup(m.Close)
	m.Start = &irc.Config{Host: "irc.gloom.im", Port: "6667", Nick: "tester", Password: "example-password"}
	m.Init()
	if m.Start.Password != "" {
		t.Fatal("startup config retained server password")
	}
}

func TestModelDropsServerPasswordFromSuppliedResult(t *testing.T) {
	conf, _ := localServer(t)
	m := New("tester", false)
	t.Cleanup(m.Close)
	result := m.connect(conf)().(connected)
	if result.err != nil {
		t.Fatal(result.err)
	}
	result.config.Password = "example-password"
	m.update(result)
	if m.conf.Password != "" {
		t.Fatal("model retained password from supplied connection result")
	}
}
