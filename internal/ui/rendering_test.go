package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestChatRowsReusedAcrossIdleUpdates(t *testing.T) {
	m := chatModel(t)
	m.View()
	first := &m.chatRows.rows[0]
	m.Update(tick(time.Now()))
	m.View()
	if &m.chatRows.rows[0] != first {
		t.Fatal("idle tick reformatted unchanged scrollback")
	}
}

func TestChatRowsInvalidateWhenContentOrStyleChanges(t *testing.T) {
	for name, change := range map[string]func(*Model){
		"message":    func(m *Model) { m.add(m.active, "alice", "another message") },
		"membership": func(m *Model) { deliver(t, m, ":irc.gloom.im 353 tester = #gloom :@alice tester") },
		"own nick":   func(m *Model) { m.command("/nick alice") },
		"timestamps": func(m *Model) { m.timestamps = !m.timestamps },
		"theme":      func(m *Model) { m.light = !m.light },
		"width":      func(m *Model) { m.Update(tea.WindowSizeMsg{Width: 50, Height: 20}) },
		"clear":      func(m *Model) { m.command("/clear") },
		"disconnect": func(m *Model) { m.disconnect() },
	} {
		t.Run(name, func(t *testing.T) {
			m := chatModel(t)
			deliver(t, m, ":irc.gloom.im 353 tester = #gloom :@alice tester")
			m.View()
			change(m)
			m.View()
			want, hits := m.messageRows(m.windows[m.active], m.colors(), m.body.w)
			if !slices.Equal(m.chatRows.rows, want) || !slices.Equal(m.chatRows.hits, hits) {
				t.Fatal("cached scrollback differs from current content")
			}
		})
	}
}

func TestWrappedArrivalsKeepScrollPosition(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "debug"}[raw], func(t *testing.T) {
			m := chatModel(t)
			if raw {
				m.toggleDebug()
			}
			add := func(text string) {
				if raw {
					m.logRaw(rawReceived, text)
				} else {
					m.add(m.active, "alice", text)
				}
			}
			for range 30 {
				add("an older line")
			}
			m.scroll(5)
			m.View()
			before := slices.Clone(m.body.rows[m.body.start : m.body.start+m.body.h])
			add(strings.Repeat("wrapped text ", 40))
			m.View()
			after := m.body.rows[m.body.start : m.body.start+m.body.h]
			if !slices.Equal(before, after) {
				t.Fatal("wrapped arrival moved the scrolled viewport")
			}
		})
	}
}

func TestRawEvictionKeepsChatSelection(t *testing.T) {
	m := chatModel(t)
	m.View()
	m.sel = selection{win: m.windows[m.active], sig: m.selSig(), moved: true}
	for range maxRaw + 1 {
		m.logRaw(rawReceived, "traffic")
	}
	if m.sel.win != m.windows[m.active] {
		t.Fatal("debug scrollback eviction cleared the chat selection")
	}
}

func TestMentionMatchesDisplayedText(t *testing.T) {
	m := New("tester", false)
	i := m.ensure("#gloom")
	m.add(i, "alice", "hello\x1b]0;tester\x07")
	if w := m.windows[i]; w.mention || w.lines[0].text != "hello" {
		t.Fatal("discarded terminal payload triggered a mention")
	}
	m.add(i, "alice", "te\x1b[31mster")
	if !m.windows[i].mention {
		t.Fatal("visible nickname did not trigger a mention")
	}
}

func TestNickChangePreservesPrefixesWithDifferentCase(t *testing.T) {
	m := chatModel(t)
	deliver(t, m, ":irc.gloom.im 353 tester = #gloom :@Alice tester")
	deliver(t, m, ":alice!u@gloom.im NICK bob")
	if got := m.windows[m.active].nicks["bob"]; got != "@bob" {
		t.Fatalf("renamed member = %q, want @bob", got)
	}
}

func TestCompletionUsesCursorAndPreservesFollowingText(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		cursor      int
		wantCursor  int
	}{
		{"/jo later", "/join later", 3, 5},
		{"你好 al later", "你好 alice later", 5, 8},
		{"alXYZ later", "alice later", 2, 5},
		{"hello al", "hello alice ", 8, 12},
	} {
		t.Run(tc.input, func(t *testing.T) {
			m := New("tester", false)
			m.windows[0].nicks["alice"] = "alice"
			m.input.SetValue(tc.input)
			m.input.SetCursor(tc.cursor)
			m.complete()
			if m.input.Value() != tc.want || m.input.Position() != tc.wantCursor {
				t.Fatalf("completion = %q at %d, want %q at %d", m.input.Value(), m.input.Position(), tc.want, tc.wantCursor)
			}
		})
	}
}

func TestThemePreparationRunsAsCommand(t *testing.T) {
	for _, gfx := range []graphics{gfxKitty, gfxITerm2} {
		m := New("tester", false)
		m.gfx = gfx
		m.update(m.renderSprites()())
		m.drawLogo()
		m.light = true
		cmd := m.scheduleLogo()
		if cmd == nil || !m.pngsPending {
			t.Fatal("theme switch did not schedule image preparation")
		}
		if len(m.sprites) != 15 && gfx == gfxKitty {
			t.Fatal("new theme was encoded synchronously")
		}
		// An earlier resize timer must not encode the new theme itself.
		m.update(logoDraw{gen: m.logoGen})
		if m.themeReady() {
			t.Fatal("resize timer encoded the new theme before its command ran")
		}
		m.update(cmd())
		if !m.themeReady() || m.pngsPending {
			t.Fatal("prepared theme is unavailable")
		}
		m.drawLogo()
		m.light = false
		if !m.themeReady() || strings.Contains(m.logoSequence(), "a=t") {
			t.Fatal("switching back did not reuse the original theme")
		}
		if len(ansi.Strip(m.View().Content)) == 0 {
			t.Fatal("empty view after theme switch")
		}
	}
}
