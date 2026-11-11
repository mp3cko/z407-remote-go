package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTimers records scheduled callbacks so tests decide when time passes.
type fakeTimers struct {
	mu      sync.Mutex
	pending []*fakeTimer
	delays  []time.Duration
}

type fakeTimer struct {
	f       func()
	stopped bool
}

func (ft *fakeTimers) after(d time.Duration, f func()) func() {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{f: f}
	ft.pending = append(ft.pending, t)
	ft.delays = append(ft.delays, d)
	return func() {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		t.stopped = true
	}
}

// elapse fires every timer that was not stopped and reports how many ran.
func (ft *fakeTimers) elapse() int {
	ft.mu.Lock()
	due := ft.pending
	ft.pending = nil
	ft.mu.Unlock()
	n := 0
	for _, t := range due {
		ft.mu.Lock()
		stopped := t.stopped
		ft.mu.Unlock()
		if !stopped {
			t.f()
			n++
		}
	}
	return n
}

// fireAll runs every timer, even stopped ones, like a timer that fired just
// as it was being cancelled.
func (ft *fakeTimers) fireAll() {
	ft.mu.Lock()
	due := ft.pending
	ft.pending = nil
	ft.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

func newTestPresence() (*Presence, *fakeTimers, *int) {
	idle := 0
	p := NewPresence(0, func() { idle++ })
	ft := &fakeTimers{}
	p.after = ft.after
	return p, ft, &idle
}

func TestPresenceLastPageClosed(t *testing.T) {
	p, ft, idle := newTestPresence()
	p.Arm()
	p.join()
	if ft.elapse() != 0 || *idle != 0 {
		t.Fatal("nothing may happen while a page is open")
	}
	p.leave()
	if len(ft.delays) != 1 || ft.delays[0] != DefaultGrace {
		t.Fatalf("grace timers: %v", ft.delays)
	}
	if *idle != 0 {
		t.Fatal("must wait for the grace period")
	}
	ft.elapse()
	if *idle != 1 {
		t.Fatalf("idle called %d times", *idle)
	}
}

func TestPresenceReloadKeepsRunning(t *testing.T) {
	p, ft, idle := newTestPresence()
	p.Arm()
	p.join()
	p.leave() // the old page goes away...
	p.join()  // ...and the reloaded one arrives within the grace period
	if n := ft.elapse(); n != 0 {
		t.Fatalf("%d timers survived the reload", n)
	}
	// Even a timer that fires while being cancelled must not stop the app.
	p.leave()
	p.join()
	ft.fireAll()
	if *idle != 0 {
		t.Fatal("a reload stopped the app")
	}
	p.leave()
	ft.elapse()
	if *idle != 1 {
		t.Fatalf("idle called %d times after the final close", *idle)
	}
}

func TestPresenceSeveralPages(t *testing.T) {
	p, ft, idle := newTestPresence()
	p.Arm()
	p.join()
	p.join()
	p.leave()
	if len(ft.delays) != 0 {
		t.Fatal("a timer started while a page is still open")
	}
	p.leave()
	ft.elapse()
	if *idle != 1 {
		t.Fatalf("idle called %d times", *idle)
	}
}

func TestPresenceUnarmedNeverFires(t *testing.T) {
	p, ft, idle := newTestPresence()
	p.join()
	p.leave()
	ft.elapse()
	if *idle != 0 {
		t.Fatal("an unarmed Presence stopped the app")
	}
	// Armed later: still nothing until a page has come and gone again.
	p.Arm()
	if ft.elapse() != 0 || *idle != 0 {
		t.Fatal("arming alone must not stop the app")
	}
}

func TestPresenceFiresOnce(t *testing.T) {
	p, ft, idle := newTestPresence()
	p.Arm()
	for i := 0; i < 2; i++ {
		p.join()
		p.leave()
		ft.elapse()
	}
	if *idle != 1 {
		t.Fatalf("idle called %d times", *idle)
	}
}

func TestPresenceRealTimer(t *testing.T) {
	done := make(chan struct{})
	p := NewPresence(10*time.Millisecond, func() { close(done) })
	p.Arm()
	p.join()
	p.leave()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("idle callback never ran")
	}
}

// openStream starts an event stream request from remote (sent to local) and
// returns a function that closes it and waits for the handler.
func openStream(t *testing.T, h http.Handler, remote, local string) (rec *httptest.ResponseRecorder, closeStream func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if local != "" {
		addr, err := net.ResolveTCPAddr("tcp", local)
		if err != nil {
			t.Fatal(err)
		}
		ctx = context.WithValue(ctx, http.LocalAddrContextKey, net.Addr(addr))
	}
	req := httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx)
	req.RemoteAddr = remote
	rec = httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(rec, req) }()
	return rec, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("event stream did not end with its request")
		}
	}
}

func waitClients(t *testing.T, p *Presence, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		n := p.clients
		p.mu.Unlock()
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("clients = %d, want %d", n, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEventsLocalPageDecidesLifetime(t *testing.T) {
	p, ft, idle := newTestPresence()
	p.Arm()
	h := New(&fakeSpeaker{}, quiet, WithPresence(p))

	rec, closeStream := openStream(t, h, "127.0.0.1:50000", "127.0.0.1:5000")
	waitClients(t, p, 1)
	closeStream()
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q", ct)
	}
	if !rec.Flushed || !strings.Contains(rec.Body.String(), "retry:") {
		t.Errorf("stream not started: flushed=%v body=%q", rec.Flushed, rec.Body)
	}
	waitClients(t, p, 0)
	ft.elapse()
	if *idle != 1 {
		t.Fatalf("closing the last local page: idle called %d times", *idle)
	}
}

func TestEventsRemoteClientsAreIgnored(t *testing.T) {
	p, ft, idle := newTestPresence()
	p.Arm()
	h := New(&fakeSpeaker{}, quiet, WithPresence(p))

	// A phone on the LAN neither keeps the app alive nor stops it.
	_, closePhone := openStream(t, h, "192.168.1.50:40000", "192.168.1.7:5000")
	_, closeLocal := openStream(t, h, "[::1]:50001", "[::1]:5000")
	waitClients(t, p, 1)
	closeLocal()
	waitClients(t, p, 0)
	ft.elapse()
	if *idle != 1 {
		t.Fatalf("an open page on another machine kept the app alive (idle=%d)", *idle)
	}
	closePhone()

	p, ft, idle = newTestPresence()
	p.Arm()
	h = New(&fakeSpeaker{}, quiet, WithPresence(p))
	_, closePhone = openStream(t, h, "192.168.1.50:40000", "192.168.1.7:5000")
	closePhone()
	if len(ft.delays) != 0 || *idle != 0 {
		t.Fatal("a page on another machine stopped the app")
	}
}

func TestEventsOwnLANAddressIsLocal(t *testing.T) {
	p, _, _ := newTestPresence()
	h := New(&fakeSpeaker{}, quiet, WithPresence(p))
	// The page was opened through this machine's own LAN address (--ip).
	_, closeStream := openStream(t, h, "192.168.1.7:50000", "192.168.1.7:5000")
	waitClients(t, p, 1)
	closeStream()
}

func TestEventsWithoutPresence(t *testing.T) {
	_, closeStream := openStream(t, New(&fakeSpeaker{}, quiet), "127.0.0.1:50000", "")
	closeStream()
}

func TestSameMachine(t *testing.T) {
	for _, c := range []struct {
		remote, local string
		want          bool
	}{
		{"127.0.0.1:1", "", true},
		{"127.8.9.1:1", "", true},
		{"[::1]:1", "", true},
		{"[fe80::1%eth0]:1", "[fe80::1%eth0]:5000", true},
		{"192.168.1.50:1", "", false},
		{"192.168.1.50:1", "192.168.1.7:5000", false},
		{"192.168.1.7:1", "192.168.1.7:5000", true},
		{"garbage", "192.168.1.7:5000", false},
		{"", "", false},
	} {
		ctx := context.Background()
		if c.local != "" {
			ctx = context.WithValue(ctx, http.LocalAddrContextKey, net.Addr(strAddr(c.local)))
		}
		req := httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx)
		req.RemoteAddr = c.remote
		if got := sameMachine(req); got != c.want {
			t.Errorf("remote %q local %q: %v", c.remote, c.local, got)
		}
	}
}

type strAddr string

func (a strAddr) Network() string { return "tcp" }
func (a strAddr) String() string  { return string(a) }

// The event stream must work through the real server: the access log wraps
// the writer, and flushing has to reach the connection.
func TestEventsOverHTTP(t *testing.T) {
	p, ft, idle := newTestPresence()
	p.Arm()
	ts := httptest.NewServer(New(&fakeSpeaker{}, quiet, WithPresence(p)))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if n, err := resp.Body.Read(buf); err != nil || !strings.HasPrefix(string(buf[:n]), "retry:") {
		t.Fatalf("first bytes %q, err %v", buf[:n], err)
	}
	waitClients(t, p, 1)
	cancel()
	resp.Body.Close()
	waitClients(t, p, 0)
	ft.elapse()
	if *idle != 1 {
		t.Fatalf("idle called %d times", *idle)
	}
}

func TestStaticFiles(t *testing.T) {
	h := New(&fakeSpeaker{}, quiet)
	for name, ctype := range map[string]string{
		"/manifest.webmanifest":  "application/manifest+json",
		"/sw.js":                 "text/javascript; charset=utf-8",
		"/icon-192.png":          "image/png",
		"/icon-512.png":          "image/png",
		"/icon-maskable-512.png": "image/png",
	} {
		rec := do(h, "GET", name)
		if rec.Code != 200 || rec.Body.Len() == 0 || rec.Header().Get("Content-Type") != ctype {
			t.Errorf("%s: code=%d type=%q len=%d", name, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		}
	}
	if cc := do(h, "GET", "/sw.js").Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("service worker cache-control = %q", cc)
	}
}

// Every icon the manifest names must be served, and the page must link the manifest.
func TestManifest(t *testing.T) {
	h := New(&fakeSpeaker{}, quiet)
	var m struct {
		Name    string
		Display string
		Icons   []struct{ Src, Sizes string }
	}
	if err := json.Unmarshal(do(h, "GET", "/manifest.webmanifest").Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name == "" || m.Display != "standalone" {
		t.Errorf("manifest: %+v", m)
	}
	sizes := map[string]bool{}
	for _, ic := range m.Icons {
		sizes[ic.Sizes] = true
		if rec := do(h, "GET", "/"+ic.Src); rec.Code != 200 {
			t.Errorf("icon %s: %d", ic.Src, rec.Code)
		}
	}
	if !sizes["192x192"] || !sizes["512x512"] {
		t.Errorf("icon sizes: %v", sizes)
	}
	page := do(h, "GET", "/").Body.String()
	for _, w := range []string{`rel="manifest" href="manifest.webmanifest"`, "serviceWorker.register('sw.js')", "new EventSource('/api/events')"} {
		if !strings.Contains(page, w) {
			t.Errorf("page lacks %q", w)
		}
	}
}
