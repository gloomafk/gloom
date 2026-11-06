package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestExitFromEveryView(t *testing.T) {
	for name, setup := range map[string]func(*Model){
		"chat":         func(m *Model) {},
		"debug":        func(m *Model) { m.toggleDebug() },
		"channel list": func(m *Model) { m.toggleView(viewList) },
		"config":       func(m *Model) { m.toggleView(viewConfig) },
		"splash":       func(m *Model) { m.view = viewSplash },
		"layout menu":  func(m *Model) { m.layMenu = true },
	} {
		for _, input := range []string{"/exit", "/EXIT heading out"} {
			t.Run(name+input, func(t *testing.T) {
				m := New("tester", false)
				m.gfx = gfxNone
				setup(m)
				for _, r := range input {
					m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
				}
				_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				if cmd == nil {
					t.Fatal("Enter did not schedule process exit")
				}
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Fatal("Enter did not signal process exit")
				}
			})
		}
	}
}

func TestQuitDoesNotExitGloom(t *testing.T) {
	for _, view := range []string{"chat", "debug"} {
		m := New("tester", false)
		m.gfx = gfxNone
		if view == "debug" {
			m.toggleDebug()
		}
		for _, r := range "/quit" {
			m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd != nil {
			if _, ok := cmd().(tea.QuitMsg); ok {
				t.Fatalf("/quit exited gloom from the %s window", view)
			}
		}
		if m.quitting {
			t.Fatalf("/quit marked gloom as quitting from the %s window", view)
		}
	}
}
