package ui

import "testing"

func TestInviteRepliesUseServerWindow(t *testing.T) {
	for _, active := range []string{"#gloom", "bob"} {
		t.Run(active, func(t *testing.T) {
			for _, tc := range []struct{ code, params, want string }{
				{"341", "alice #gloom", "alice #gloom"},
				{"401", "alice :No such nick", "alice No such nick"},
				{"442", "#gloom :You're not on that channel", "#gloom You're not on that channel"},
				{"443", "alice #gloom :is already on channel", "alice #gloom is already on channel"},
				{"482", "#gloom :You're not a channel operator", "#gloom You're not a channel operator"},
				{"461", "INVITE :Not enough parameters", "INVITE Not enough parameters"},
			} {
				t.Run(tc.code, func(t *testing.T) {
					m := New("tester", false)
					server := m.openServer("irc.gloom.im")
					i := m.ensure(active)
					m.selectWindow(i)
					before := len(m.windows[i].lines)
					status := len(m.windows[0].lines)
					windows := len(m.windows)
					deliver(t, m, ":irc.gloom.im "+tc.code+" tester "+tc.params)
					lines := m.windows[server].lines
					if len(lines) != 1 || lines[0].code != tc.code || lines[0].text != tc.want {
						t.Fatalf("server lines = %+v, want %s %q", lines, tc.code, tc.want)
					}
					if m.active != i || len(m.windows) != windows || len(m.windows[i].lines) != before || len(m.windows[0].lines) != status {
						t.Fatal("invite reply changed another window")
					}
				})
			}
		})
	}
}
