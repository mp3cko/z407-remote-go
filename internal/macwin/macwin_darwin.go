//go:build darwin && cgo

package macwin

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit

#include <stdlib.h>

void macwin_run(const char *url, const char *title, int width, int height, const void *icon, int iconLen);
void macwin_close(void);
void macwin_stop(void);
*/
import "C"

import (
	"runtime"
	"sync"
	"unsafe"
)

// init keeps the main goroutine on the main thread, where Cocoa must run.
func init() { runtime.LockOSThread() }

type request struct {
	url, title    string
	width, height int
	icon          []byte
}

var (
	mu      sync.Mutex
	ui      chan request // set while Main waits for a window to open
	current *Window
	running bool // the Cocoa run loop has been started
)

// Main runs run in the background and keeps the main thread for the window.
// It returns when run has returned. It must be called from main.
func Main(run func()) {
	req := make(chan request, 1)
	mu.Lock()
	ui = req
	mu.Unlock()
	done := make(chan struct{})
	go func() {
		run()
		mu.Lock()
		ui = nil
		stop := running
		mu.Unlock()
		close(done)
		if stop {
			C.macwin_stop()
		}
	}()
	select {
	case <-done:
	case r := <-req:
		url, title := C.CString(r.url), C.CString(r.title)
		defer C.free(unsafe.Pointer(url))
		defer C.free(unsafe.Pointer(title))
		var icon unsafe.Pointer
		if len(r.icon) > 0 {
			icon = C.CBytes(r.icon)
			defer C.free(icon)
		}
		// Runs the window until run has returned and stopped it.
		C.macwin_run(url, title, C.int(r.width), C.int(r.height), icon, C.int(len(r.icon)))
		<-done
	}
}

// Available reports whether Open can open a window: while Main runs and no
// window was opened yet.
func Available() bool {
	mu.Lock()
	defer mu.Unlock()
	return ui != nil
}

// Open shows url in a window of the program's own. There is one window per
// run; closing it ends Wait.
func Open(url, title string, width, height int, icon []byte) (*Window, error) {
	mu.Lock()
	defer mu.Unlock()
	if ui == nil {
		return nil, ErrUnavailable
	}
	current = newWindow()
	running = true
	ui <- request{url, title, width, height, icon}
	ui = nil
	return current, nil
}

func closeWindow() { C.macwin_close() }

//export goWindowClosed
func goWindowClosed() {
	mu.Lock()
	w := current
	mu.Unlock()
	if w != nil {
		w.markClosed()
	}
}
