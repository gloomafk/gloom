package ui

import (
	"io"
	"net"
	"testing"
	"time"

	"gloom.im/gloom/internal/irc"
)

func localServer(t *testing.T) (irc.Config, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		close(accepted)
		_, _ = io.Copy(io.Discard, conn)
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return irc.Config{Host: host, Port: port, Nick: "tester"}, accepted
}

func clientClosed(t *testing.T, client *irc.Client) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-client.Events:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("connection remained open")
		}
	}
}

func TestDisconnectCancelsPendingDial(t *testing.T) {
	conf, accepted := localServer(t)
	conf.TLS = true // Leave the TLS handshake waiting for the server.
	m := New("tester", false)
	t.Cleanup(m.Close)
	cmd := m.connect(conf)
	result := make(chan connected, 1)
	go func() { result <- cmd().(connected) }()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("dial never reached the server")
	}
	m.command("/disconnect")
	select {
	case v := <-result:
		if v.err == nil {
			t.Fatal("canceled dial succeeded")
		}
		m.update(v)
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel the TLS handshake")
	}
	if m.connecting || m.client != nil || m.network != "offline" {
		t.Fatal("canceled connection changed the disconnected state")
	}
}

func TestCloseCleansUpUndeliveredConnection(t *testing.T) {
	conf, _ := localServer(t)
	m := New("tester", false)
	t.Cleanup(m.Close)
	v := m.connect(conf)().(connected)
	if v.err != nil {
		t.Fatal(v.err)
	}
	m.Close()
	clientClosed(t, v.client)
}

func TestLateConnectionDoesNotReplaceNewAttempt(t *testing.T) {
	conf, _ := localServer(t)
	m := New("tester", false)
	t.Cleanup(m.Close)
	v := m.connect(conf)().(connected)
	if v.err != nil {
		t.Fatal(v.err)
	}
	m.disconnect()
	m.connect(conf)
	ctx := m.connectCtx
	m.update(v)
	if !m.connecting || m.connectCtx != ctx || m.client != nil {
		t.Fatal("stale result replaced the newer connection attempt")
	}
	clientClosed(t, v.client)
}

func TestQuitRetainsSocketUntilCompletion(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "shutdown"}[shutdown], func(t *testing.T) {
			conf, _ := localServer(t)
			m := New("tester", false)
			t.Cleanup(m.Close)
			v := m.connect(conf)().(connected)
			if v.err != nil {
				t.Fatal(v.err)
			}
			m.update(v)
			cmd := m.quit("goodbye")
			if m.leaving != v.client || m.connect(conf) != nil {
				t.Fatal("reconnect allowed while QUIT still owns the socket")
			}
			if shutdown {
				m.Close()
			} else {
				m.update(cmd())
				if m.leaving != nil {
					t.Fatal("QUIT completion did not release the socket")
				}
			}
			clientClosed(t, v.client)
		})
	}
}

func TestDisconnectClearsServerSpecificState(t *testing.T) {
	m := New("tester", false)
	m.listing = true
	m.list = []listEntry{{name: "#old"}}
	m.listIdx = []int{0}
	m.pingAt = time.Now()
	m.disconnect()
	if m.listing || len(m.list) != 0 || len(m.listIdx) != 0 || !m.pingAt.IsZero() {
		t.Fatal("previous server state survived disconnect")
	}
}
