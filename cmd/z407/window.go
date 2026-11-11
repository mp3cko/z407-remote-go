package main

import (
	"context"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/appwin"
	"github.com/mp3cko/z407-remote-go/internal/logx"
	"github.com/mp3cko/z407-remote-go/internal/server"
)

// earlyExit is how soon after its start a failing browser counts as "the
// window never opened" rather than "the window was closed".
const earlyExit = 3 * time.Second

// settle is how long after the browser process ended the page gets to
// disappear (or, when the window lives on elsewhere, to still be there).
const settle = 250 * time.Millisecond

// windowOpener shows the remote on this machine. appwin.System is the real one.
type windowOpener interface {
	// OpenApp opens url in an app window owned by the returned process.
	OpenApp(url string, port int) (appwin.Process, error)
	// OpenDefault opens url as a tab in the default browser.
	OpenDefault(url string) error
}

// localURL is the address of the remote as seen from this machine.
func localURL(bindIP string, port int) string {
	host := bindIP
	if ip := net.ParseIP(bindIP); bindIP == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// window ties the lifetime of the app to a window showing the remote.
type window struct {
	win      windowOpener
	log      *logx.Logger
	presence *server.Presence
	quit     func() // stops the app
	early    time.Duration
}

// open shows url and returns a function that closes the window again (a
// no-op when there is nothing to close). Preferred is an app window, whose
// browser process ends when the window is closed. Without a Chromium browser
// the page opens in the default browser and the app stops once the last page
// on this machine is gone. The same happens when the browser hands the
// window to a copy of itself that is already running and exits: the window
// is there, but no process of ours stands for it. ctx is the app's own context.
func (w window) open(ctx context.Context, url string, port int) (closeWindow func()) {
	proc, err := w.win.OpenApp(url, port)
	switch {
	case err == nil:
		started := time.Now()
		exited := make(chan struct{})
		go func() {
			werr := proc.Wait()
			close(exited)
			if ctx.Err() != nil {
				return // the app is stopping and closed the window itself
			}
			if werr != nil && time.Since(started) < w.early {
				w.log.Errorf("The app window did not open (%v)", werr)
				w.openTab(url)
				return
			}
			if werr == nil && w.handedOff(ctx, started) {
				w.presence.Arm()
				w.log.Infof("The window is run by a browser that was already open. The app stops when you close the window (or press Ctrl+C)")
				return
			}
			w.log.Infof("Window closed, stopping")
			w.quit()
		}()
		return func() {
			select {
			case <-exited:
			default:
				_ = proc.Close()
			}
		}
	case errors.Is(err, appwin.ErrNoDisplay):
		w.log.Infof("No display found: running without a window (as with --no-window)")
	case errors.Is(err, appwin.ErrNoBrowser):
		w.openTab(url)
	default:
		w.log.Errorf("Could not open the app window: %v", err)
		w.openTab(url)
	}
	return func() {}
}

// handedOff reports whether the window is still there although the browser
// process has ended cleanly. That happens when a browser with the same
// profile was already running, for instance one left over from the previous
// run: the new process passes the window on to it and exits at once. A page
// that is open on this machine tells the two cases apart. Shortly after the
// start the page may still be loading, so it gets until the early limit.
func (w window) handedOff(ctx context.Context, started time.Time) bool {
	deadline := started.Add(w.early)
	wait := min(settle, w.early)
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
		if w.presence.Present() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
	}
}

func (w window) openTab(url string) {
	if err := w.win.OpenDefault(url); err != nil {
		w.log.Errorf("Could not open a browser (%v). Open %s yourself; stop the app with Ctrl+C", err, url)
		return
	}
	w.presence.Arm()
	w.log.Infof("Opened %s in your browser. The app stops when you close that page (or press Ctrl+C)", url)
}
