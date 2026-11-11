package main

import (
	"io"
	"os"

	"github.com/charmbracelet/x/ansi"
)

// startupOutput reports alternate-screen entry only after it reaches the output.
// Bubble Tea flushes raw commands before frames, so a View alone is not proof
// that graphics can safely be uploaded to the alternate screen.
type startupOutput struct {
	*os.File
	io.Writer
	ready   func()
	matched int
	done    bool
}

// Keep the full terminal-file contract used by Bubble Tea for size and modes.
var _ interface {
	io.ReadWriteCloser
	Fd() uintptr
} = (*startupOutput)(nil)

func (w *startupOutput) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if w.done {
		return n, err
	}
	for _, b := range p[:n] {
		if b != ansi.SetModeAltScreenSaveCursor[w.matched] {
			w.matched = 0
		}
		if b == ansi.SetModeAltScreenSaveCursor[w.matched] {
			w.matched++
		}
		if w.matched == len(ansi.SetModeAltScreenSaveCursor) {
			w.done = true
			// Send must not block the renderer while it holds its output lock.
			go w.ready()
			break
		}
	}
	return n, err
}
