package ui

import (
	"fmt"
	"testing"

	"gloom.im/gloom/internal/irc"
)

// BenchmarkHistoryArrival measures a parsed arrival followed by a complete view.
// Both channels have full histories and 300 members at 120 by 40 cells.
func BenchmarkHistoryArrival(b *testing.B) {
	const text = "hello world this is a fairly long chat line with some more words to make it wrap around the pane"
	for _, mode := range []string{"idle", "active", "background", "debug"} {
		b.Run(mode, func(b *testing.B) {
			m := New("tester", false)
			m.width, m.height, m.gfx = 120, 40, gfxNone
			m.ghost.frozen = true
			a, z := m.ensure("#gloom"), m.ensure("#other")
			for _, i := range []int{a, z} {
				for n := range 300 {
					nick := fmt.Sprintf("member%03d", n)
					m.windows[i].nicks[nick] = nick
				}
				for range maxLines {
					m.add(i, "user", text)
				}
			}
			m.selectWindow(a)
			raw := ":user!u@gloom.im PRIVMSG #gloom :" + text
			msg, err := irc.ParseMessage(raw)
			if err != nil {
				b.Fatal(err)
			}
			if mode == "background" {
				msg.Params[0] = "#other"
			}
			if mode == "debug" {
				m.toggleDebug()
				for range maxRaw {
					m.logRaw(rawReceived, raw)
				}
			}
			m.View()
			b.ReportAllocs()
			for b.Loop() {
				switch mode {
				case "active", "background":
					m.receive(irc.Event{Message: msg})
				case "debug":
					m.logRaw(rawReceived, raw)
				}
				m.View()
			}
		})
	}
}
