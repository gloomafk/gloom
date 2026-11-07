package ui

import (
	"bufio"
	"net"
	"slices"
	"testing"
	"time"

	"gloom.im/gloom/internal/irc"
)

// closeModel returns a model connected to a loopback server with a server
// window, two joined channels, two private message windows and the debug
// window. sent returns the next line the server receives.
func closeModel(t *testing.T) (m *Model, sent func() string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	received := make(chan string, 64)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			received <- scanner.Text()
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	conf := irc.Config{Host: host, Port: port, Nick: "tester"}
	client, err := irc.Dial(t.Context(), conf)
	if err != nil {
		t.Fatal(err)
	}
	sent = func() string {
		t.Helper()
		select {
		case s := <-received:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for a line")
			return ""
		}
	}
	sent() // NICK
	sent() // USER
	m = New("tester", false)
	t.Cleanup(m.Close)
	m.openServer("irc.gloom.im")
	m.client, m.conf = client, conf
	for _, name := range []string{"#one", "#two"} {
		deliver(t, m, ":tester!u@gloom.im JOIN "+name)
	}
	deliver(t, m, ":alice!u@gloom.im PRIVMSG tester :hi")
	deliver(t, m, ":bob!u@gloom.im PRIVMSG tester :hey")
	m.toggleDebug()
	return m, sent
}

func windowNames(m *Model) []string {
	var names []string
	for _, w := range m.windows {
		names = append(names, w.name)
	}
	return names
}

func TestCloseCurrentWindow(t *testing.T) {
	m, sent := closeModel(t)
	m.selectWindow(m.find("#one"))
	m.command("/close")
	if got := sent(); got != "PART #one" {
		t.Fatalf("server received %q, want PART #one", got)
	}
	if m.find("#one") >= 0 {
		t.Fatal("#one stayed open")
	}
	m.selectWindow(m.find("alice"))
	m.command("/close")
	if m.find("alice") >= 0 {
		t.Fatal("the alice window stayed open")
	}
	want := []string{"gloom", "irc.gloom.im", "#two", "bob", "debug"}
	if got := windowNames(m); !slices.Equal(got, want) {
		t.Fatalf("windows %q, want %q", got, want)
	}
}

func TestCloseKeepsGloomWindow(t *testing.T) {
	m := New("tester", false)
	m.command("/close")
	if len(m.windows) != 1 || m.active != 0 {
		t.Fatal("the gloom window closed")
	}
	if got := m.windows[0].lines[len(m.windows[0].lines)-1].text; got != "The gloom window cannot be closed." {
		t.Fatalf("last note %q", got)
	}
}

func TestCloseAllChannels(t *testing.T) {
	m, sent := closeModel(t)
	m.selectWindow(m.find("alice"))
	m.command("/close -c")
	for _, want := range []string{"PART #two", "PART #one"} {
		if got := sent(); got != want {
			t.Fatalf("server received %q, want %q", got, want)
		}
	}
	want := []string{"gloom", "irc.gloom.im", "alice", "bob", "debug"}
	if got := windowNames(m); !slices.Equal(got, want) {
		t.Fatalf("windows %q, want %q", got, want)
	}
	if m.current().name != "alice" {
		t.Fatalf("selected %q, want alice to stay selected", m.current().name)
	}
	if m.client == nil {
		t.Fatal("closing channels left the server")
	}
}

func TestCloseAllMessages(t *testing.T) {
	m, _ := closeModel(t)
	m.selectWindow(m.find("bob"))
	m.command("/close -m")
	want := []string{"gloom", "irc.gloom.im", "#one", "#two", "debug"}
	if got := windowNames(m); !slices.Equal(got, want) {
		t.Fatalf("windows %q, want %q", got, want)
	}
	if m.active < 0 || m.active >= len(m.windows) || m.current().raw {
		t.Fatalf("active window %d is invalid", m.active)
	}
}

func TestCloseRejectsUnknownFlag(t *testing.T) {
	m, _ := closeModel(t)
	before := windowNames(m)
	m.command("/close -x")
	if got := windowNames(m); !slices.Equal(got, before) {
		t.Fatalf("windows changed to %q", got)
	}
	w := m.current()
	if got := w.lines[len(w.lines)-1].text; got != "usage: /close [-c | -m]" {
		t.Fatalf("last note %q", got)
	}
}

func TestCloseServerWindowLeavesServer(t *testing.T) {
	m, sent := closeModel(t)
	client := m.client
	m.selectWindow(m.find("irc.gloom.im"))
	cmd := m.command("/close")
	if cmd == nil {
		t.Fatal("closing the server window did not quit")
	}
	if _, ok := cmd().(quitDone); !ok {
		t.Fatal("closing the server window did not run the quit")
	}
	if got := sent(); got != "QUIT" {
		t.Fatalf("server received %q, want QUIT", got)
	}
	clientClosed(t, client)
	want := []string{"gloom", "debug"}
	if got := windowNames(m); !slices.Equal(got, want) {
		t.Fatalf("windows %q, want %q", got, want)
	}
	if m.active != 0 || m.client != nil || m.network != "offline" {
		t.Fatal("expected the gloom window, disconnected")
	}
	if m.srv != nil || m.serverWindow() != 0 {
		t.Fatal("the server window is still tracked")
	}
}

func TestCloseServerWindowWhileDisconnected(t *testing.T) {
	m := New("tester", false)
	m.openServer("irc.gloom.im")
	m.selectWindow(m.ensure("#old"))
	m.selectWindow(1)
	if cmd := m.command("/close"); cmd != nil {
		t.Fatal("closing a disconnected server window returned a command")
	}
	if got := windowNames(m); !slices.Equal(got, []string{"gloom"}) || m.active != 0 {
		t.Fatalf("windows %q, active %d", got, m.active)
	}
}
