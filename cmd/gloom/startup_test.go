package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"gloom.im/gloom/internal/ui"
)

func TestStartupOutputSplitSequence(t *testing.T) {
	var buf bytes.Buffer
	ready := make(chan struct{}, 2)
	w := &startupOutput{Writer: &buf, ready: func() { ready <- struct{}{} }}
	for _, b := range []byte(ansi.SetModeAltScreenSaveCursor) {
		if _, err := w.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("alternate screen not reported")
	}
	if buf.String() != ansi.SetModeAltScreenSaveCursor {
		t.Fatal("output changed")
	}
	if _, err := w.Write([]byte(ansi.SetModeAltScreenSaveCursor)); err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Fatal("duplicate report")
	}
}

func TestStartupOutputIgnoresUnwrittenBytes(t *testing.T) {
	w := &startupOutput{Writer: outputFunc(func(b []byte) (int, error) {
		return len(b) - 1, io.ErrShortWrite
	}), ready: func() { t.Error("reported unwritten screen entry") }}
	if _, err := w.Write([]byte(ansi.SetModeAltScreenSaveCursor)); err != io.ErrShortWrite {
		t.Fatal("write error was not preserved")
	}
	if w.done {
		t.Fatal("incomplete screen entry completed startup")
	}
}

func TestStartupOutputOrdersGraphicsAfterAltScreen(t *testing.T) {
	t.Setenv("GLOOM_GRAPHICS", "kitty")
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("TMUX", "")
	f, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	drawn := make(chan struct{}, 1)
	output := &startupOutput{Writer: outputFunc(func(b []byte) (int, error) {
		n, err := f.Write(b)
		if bytes.Contains(b[:n], []byte("\U0010eeee")) {
			select {
			case drawn <- struct{}{}:
			default:
			}
		}
		return n, err
	}), File: f}
	m := ui.New("tester", false)
	defer m.Close()
	// A slow renderer makes image preparation finish before its first flush.
	// This reproduces the ordering that a fast CPU can hit at normal FPS.
	p := tea.NewProgram(m, tea.WithOutput(output), tea.WithInput(nil),
		tea.WithWindowSize(140, 45), tea.WithFPS(1), tea.WithContext(ctx),
		tea.WithoutSignalHandler())
	go func() {
		select {
		case <-drawn:
			p.Quit()
		case <-ctx.Done():
		}
	}()
	output.ready = func() { p.Send(tea.ModeReportMsg{Mode: ansi.ModeAltScreenSaveCursor, Value: ansi.ModeSet}) }
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	alt, upload, placement := strings.Index(s, ansi.SetModeAltScreenSaveCursor), strings.Index(s, "a=t"), strings.Index(s, "a=p,U=1")
	if alt < 0 || upload <= alt || placement <= upload {
		t.Fatalf("output order: alt=%d upload=%d placement=%d", alt, upload, placement)
	}
	if strings.Count(s, "a=t") != 15 {
		t.Fatal("missing or repeated uploads")
	}
}

type outputFunc func([]byte) (int, error)

func (f outputFunc) Write(b []byte) (int, error) { return f(b) }
