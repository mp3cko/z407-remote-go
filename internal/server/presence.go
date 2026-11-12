package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultGrace is how long the last page may be gone before the app stops.
// It has to cover a page reload, where the old connection closes a moment
// before the new one opens.
const DefaultGrace = 5 * time.Second

// Presence tracks the pages open on this machine through their event streams
// (GET /api/events). Once armed, it calls onIdle when the last of them has
// been gone for the grace period. Pages on other machines are never counted.
//
// It is used when the remote runs in a normal browser tab, where no browser
// process can tell the app that the page was closed.
type Presence struct {
	grace  time.Duration
	onIdle func()
	// after schedules f and returns a function cancelling it (time.AfterFunc in production).
	after func(d time.Duration, f func()) (stop func())

	mu      sync.Mutex
	clients int
	armed   bool
	fired   bool
	gen     int // invalidates timers started before the latest change
	stop    func()
}

// NewPresence returns an unarmed Presence. A grace of 0 means DefaultGrace.
func NewPresence(grace time.Duration, onIdle func()) *Presence {
	if grace <= 0 {
		grace = DefaultGrace
	}
	return &Presence{
		grace:  grace,
		onIdle: onIdle,
		after: func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		},
	}
}

// Arm makes the Presence act. Until the first local page has connected and
// left again nothing happens, so a browser that is slow to start is fine.
func (p *Presence) Arm() {
	p.mu.Lock()
	p.armed = true
	p.mu.Unlock()
}

// Present reports whether a page is open on this machine right now.
func (p *Presence) Present() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clients > 0
}

func (p *Presence) join() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients++
	p.cancelTimer()
}

func (p *Presence) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients--
	if p.clients > 0 {
		return
	}
	p.cancelTimer()
	gen := p.gen
	p.stop = p.after(p.grace, func() { p.expire(gen) })
}

// cancelTimer must be called with mu held.
func (p *Presence) cancelTimer() {
	p.gen++
	if p.stop != nil {
		p.stop()
		p.stop = nil
	}
}

func (p *Presence) expire(gen int) {
	p.mu.Lock()
	idle := p.armed && !p.fired && gen == p.gen && p.clients == 0
	if idle {
		p.fired = true
	}
	p.mu.Unlock()
	if idle && p.onIdle != nil {
		p.onIdle()
	}
}

// sameMachine reports whether the request comes from this computer: from a
// loopback address, or from the very address it was sent to (the page was
// opened through the machine's own LAN address).
func sameMachine(r *http.Request) bool {
	remote := hostIP(r.RemoteAddr)
	if remote == nil {
		return false
	}
	if remote.IsLoopback() {
		return true
	}
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	return ok && remote.Equal(hostIP(local.String()))
}

func hostIP(addr string) net.IP {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if i := strings.LastIndexByte(host, '%'); i >= 0 { // IPv6 zone
		host = host[:i]
	}
	return net.ParseIP(host)
}

// events is the long-lived stream every page holds open. Its existence tells
// the app that the page is still there. The one event it carries is "bye",
// sent when the app stops: the page then says so, and a page on this machine
// ("close") closes its window. The app cannot always do that itself: a
// window that the browser handed to a copy already running has no process
// the app could end.
func (s *srv) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	// A short retry makes a reloaded or briefly disconnected page come back fast.
	_, _ = w.Write([]byte("retry: 1000\n\n: connected\n\n"))
	_ = http.NewResponseController(w).Flush()

	bye := "event: bye\ndata: \n\n"
	if sameMachine(r) {
		bye = "event: bye\ndata: close\n\n"
		if s.presence != nil {
			s.presence.join()
			defer s.presence.leave()
		}
	}
	// Done also when the page has left; the write then goes nowhere.
	<-r.Context().Done()
	_, _ = w.Write([]byte(bye))
	_ = http.NewResponseController(w).Flush()
}
