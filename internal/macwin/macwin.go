// Package macwin shows the remote in a window of the program's own on macOS:
// a WebKit view in a Cocoa window, so the Dock and the app switcher show the
// app's name and icon instead of a browser's. Cocoa has to run on the main
// thread, so main hands that thread over with Main. Elsewhere Main just runs
// the program and Open reports ErrUnavailable.
package macwin

import (
	"errors"
	"sync"
)

// ErrUnavailable means there is no window of our own to open: not macOS, or
// Main is not running, or the window was opened already.
var ErrUnavailable = errors.New("no native window available")

// Window is the open window. It satisfies appwin.Process.
type Window struct {
	closed chan struct{}
	once   sync.Once
}

func newWindow() *Window { return &Window{closed: make(chan struct{})} }

// Wait blocks until the window has been closed.
func (w *Window) Wait() error {
	<-w.closed
	return nil
}

// Close closes the window.
func (w *Window) Close() error {
	closeWindow()
	return nil
}

// Owned marks the window as the program's own: when it is closed it is gone,
// never handed on to another process (see appwin.Owned).
func (w *Window) Owned() {}

func (w *Window) markClosed() { w.once.Do(func() { close(w.closed) }) }
