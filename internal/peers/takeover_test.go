package peers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mp3cko/z407-remote-anywhere/internal/logx"
)

// peerServer is another instance of the app, as far as takeover can tell.
type peerServer struct {
	*httptest.Server
	mu   sync.Mutex
	hits []string // "METHOD path"
}

func newPeer(t *testing.T) *peerServer {
	t.Helper()
	p := &peerServer{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hits = append(p.hits, r.Method+" "+r.URL.Path)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}` + "\n"))
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *peerServer) addr() string { return strings.TrimPrefix(p.URL, "http://") }
func (p *peerServer) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.hits...)
}

// unreachable returns an address nothing listens on.
func unreachable(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// rig is a Takeover with a fake clock and a recorded log.
type rig struct {
	*Takeover
	now       time.Time
	connected bool
	peers     []string
	log       []string
	levels    []logx.Level
}

func newRig(peers ...string) *rig {
	r := &rig{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), peers: peers}
	r.Takeover = &Takeover{
		Peers:     func() []string { return r.peers },
		Connected: func() bool { return r.connected },
		Log: func(l logx.Level, f string, a ...any) {
			r.levels = append(r.levels, l)
			r.log = append(r.log, f)
		},
		PeerTimeout: 500 * time.Millisecond,
	}
	return r
}

// advance moves the fake clock and runs one check.
func (r *rig) advance(d time.Duration) {
	r.now = r.now.Add(d)
	r.Check(context.Background(), r.now)
}

var disconnect = []string{"POST /api/disconnect"}

// The first round does not wait: the peers are asked as the search starts.
func TestFirstRoundIsAtOnce(t *testing.T) {
	p := newPeer(t)
	r := newRig(p.addr())
	r.advance(0)
	if !reflect.DeepEqual(p.got(), disconnect) {
		t.Fatalf("peer not asked at the start: %v", p.got())
	}
}

// A peer added after the first round waits for the search to time out.
func TestLaterPeerWaitsForTimeout(t *testing.T) {
	r := newRig()
	r.advance(0)
	p := newPeer(t)
	r.peers = []string{p.addr()}
	r.advance(SearchTimeout - time.Millisecond)
	if len(p.got()) != 0 {
		t.Fatalf("peer asked before the timeout: %v", p.got())
	}
	r.advance(time.Millisecond)
	if !reflect.DeepEqual(p.got(), disconnect) {
		t.Fatalf("peer not asked at the timeout: %v", p.got())
	}
}

func TestNoCallWhileConnected(t *testing.T) {
	r := newRig()
	r.connected = true
	r.advance(0)
	r.advance(10 * SearchTimeout)
	r.connected = false
	r.advance(0) // the first round, with nobody to ask
	r.connected = true
	r.advance(time.Second)

	p := newPeer(t)
	r.peers = []string{p.addr()}
	r.advance(10 * SearchTimeout)
	if len(p.got()) != 0 {
		t.Fatalf("peer asked while connected: %v", p.got())
	}

	// A drop starts a fresh search: the time spent connected does not count.
	r.connected = false
	r.advance(time.Second)
	r.advance(SearchTimeout - time.Second)
	if len(p.got()) != 0 {
		t.Fatalf("peer asked %v after the drop", SearchTimeout-time.Second)
	}
	// Connecting in time cancels it.
	r.connected = true
	r.advance(time.Second)
	r.connected = false
	r.advance(time.Second)
	r.advance(SearchTimeout - time.Second)
	if len(p.got()) != 0 {
		t.Fatal("the search time was not reset by a connection")
	}
	r.advance(time.Second)
	if !reflect.DeepEqual(p.got(), disconnect) {
		t.Fatalf("peer not asked after a full search: %v", p.got())
	}
}

func TestEveryPeerIsAskedAtTheSameTime(t *testing.T) {
	a, b, c := newPeer(t), newPeer(t), newPeer(t)
	r := newRig(a.addr(), b.addr(), c.addr())
	// Every request waits until all three have come in: asked one after
	// another, they would time out.
	var arrived sync.WaitGroup
	arrived.Add(3)
	r.Post = func(ctx context.Context, peer string) (string, error) {
		arrived.Done()
		arrived.Wait()
		return PostDisconnect(ctx, peer)
	}
	r.advance(0)
	for _, p := range []*peerServer{a, b, c} {
		if !reflect.DeepEqual(p.got(), disconnect) {
			t.Errorf("peer %s got %v", p.addr(), p.got())
		}
	}
	if len(r.log) != 3 || !strings.Contains(r.log[0], "to release the speakers (%s): %s") {
		t.Errorf("log: %q", r.log)
	}
}

func TestUnreachablePeerIsSkipped(t *testing.T) {
	down, up := unreachable(t), newPeer(t)
	r := newRig(down, up.addr())
	r.advance(0)
	if !reflect.DeepEqual(up.got(), disconnect) {
		t.Fatalf("the peer next to an unreachable one got %v", up.got())
	}
	if len(r.log) != 2 || !strings.Contains(strings.Join(r.log, "|"), "did not answer") {
		t.Fatalf("log: %q", r.log)
	}
	for _, l := range r.levels {
		if l == logx.Error {
			t.Error("an unreachable peer was logged as an error")
		}
	}
}

func TestSlowPeerTimesOut(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)
	up := newPeer(t)
	r := newRig(strings.TrimPrefix(slow.URL, "http://"), up.addr())
	r.PeerTimeout = 300 * time.Millisecond // short, but enough for the healthy peer on a slow machine
	start := time.Now()
	r.advance(0)
	if time.Since(start) > 5*time.Second {
		t.Fatal("a slow peer held up the takeover")
	}
	if !reflect.DeepEqual(up.got(), disconnect) {
		t.Fatalf("peer next to the slow one got %v", up.got())
	}
}

func TestOncePerRun(t *testing.T) {
	p, down := newPeer(t), unreachable(t)
	r := newRig(p.addr(), down)
	r.advance(0)
	r.advance(SearchTimeout)
	r.advance(SearchTimeout)
	r.advance(time.Hour)
	if !reflect.DeepEqual(p.got(), disconnect) {
		t.Fatalf("asked more than once while searching: %v", p.got())
	}
	// Not after a later connection loss either: an instance that lists us
	// back would be shut down again and again.
	r.connected = true
	r.advance(time.Minute)
	r.connected = false
	r.advance(time.Second)
	r.advance(2 * SearchTimeout)
	if !reflect.DeepEqual(p.got(), disconnect) {
		t.Fatalf("asked again after a reconnect: %v", p.got())
	}
	if len(r.log) != 2 {
		t.Errorf("unreachable peer retried: %q", r.log)
	}

	// A peer added in the UI while searching is asked on the next check.
	added := newPeer(t)
	r.peers = append(r.peers, added.addr())
	r.advance(time.Second)
	if !reflect.DeepEqual(added.got(), disconnect) || !reflect.DeepEqual(p.got(), disconnect) {
		t.Fatalf("after adding a peer: new %v, old %v", added.got(), p.got())
	}
}

func TestOwnAddressIsSkipped(t *testing.T) {
	self, other := newPeer(t), newPeer(t)
	r := newRig(self.addr(), other.addr())
	r.IsSelf = func(peer string) bool { return peer == self.addr() }
	r.advance(0)
	r.advance(SearchTimeout)
	r.advance(SearchTimeout)
	if len(self.got()) != 0 {
		t.Fatalf("asked itself to shut down: %v", self.got())
	}
	if !reflect.DeepEqual(other.got(), disconnect) {
		t.Fatalf("other peer got %v", other.got())
	}
	if len(r.log) != 2 || !strings.Contains(r.log[0], "is this computer") {
		t.Errorf("log: %q", r.log)
	}
}

func TestSelfCheck(t *testing.T) {
	local := func() []net.IP { return []net.IP{net.ParseIP("192.168.1.7"), net.ParseIP("fe80::7")} }
	lookup := func(host string) ([]net.IP, error) {
		switch host {
		case "my-pc":
			return []net.IP{net.ParseIP("192.168.1.7")}, nil
		case "localhost":
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		case "other-pc":
			return []net.IP{net.ParseIP("192.168.1.20")}, nil
		}
		return nil, errors.New("no such host")
	}
	isSelf := SelfCheck(5000, local, lookup)
	for peer, want := range map[string]bool{
		"127.0.0.1:5000":    true,
		"[::1]:5000":        true,
		"192.168.1.7:5000":  true,
		"[fe80::7]:5000":    true,
		"my-pc:5000":        true,
		"localhost:5000":    true,
		"127.0.0.1:5001":    false, // another instance on this machine
		"my-pc:5001":        false,
		"192.168.1.20:5000": false,
		"other-pc:5000":     false,
		"unknown-pc:5000":   false,
		"garbage":           false,
	} {
		if got := isSelf(peer); got != want {
			t.Errorf("%s: %v", peer, got)
		}
	}
}

func TestCheckStopsWhenCancelled(t *testing.T) {
	p := newPeer(t)
	r := newRig(p.addr())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Check(ctx, r.now)
	if len(p.got()) != 0 {
		t.Fatalf("asked a peer while shutting down: %v", p.got())
	}
}

func TestRun(t *testing.T) {
	p := newPeer(t)
	var mu sync.Mutex
	asked := make(chan struct{})
	tk := &Takeover{
		Peers:     func() []string { return []string{p.addr()} },
		Connected: func() bool { return false },
		Timeout:   20 * time.Millisecond,
		Post: func(ctx context.Context, peer string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			a, err := PostDisconnect(ctx, peer)
			close(asked)
			return a, err
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tk.Run(ctx, 5*time.Millisecond); close(done) }()
	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("Run never asked the peer")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if !reflect.DeepEqual(p.got(), disconnect) {
		t.Fatalf("peer got %v", p.got())
	}
}

func TestPostDisconnectAnswer(t *testing.T) {
	p := newPeer(t)
	got, err := PostDisconnect(context.Background(), p.addr())
	if err != nil || got != `200 {"success":true}` {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := PostDisconnect(context.Background(), "bad host"); err == nil {
		t.Error("expected an error for an invalid address")
	}
}

// Speakers that were seen get more time: the connection is being set up,
// and no peer must be closed for that.
func TestSeenSpeakersGetMoreTime(t *testing.T) {
	p := newPeer(t)
	r := newRig()
	found := true
	r.Found = func() bool { return found }
	r.advance(0) // the first round, before the peer was added
	r.peers = []string{p.addr()}
	r.advance(SearchTimeout)
	r.advance(ConnectTimeout - SearchTimeout - time.Millisecond)
	if len(p.got()) != 0 {
		t.Fatalf("peer asked while connecting: %v", p.got())
	}
	r.advance(time.Millisecond) // seen, but the connection never came
	if !reflect.DeepEqual(p.got(), disconnect) {
		t.Fatalf("peer got %v", p.got())
	}

	// Not seen in the next attempt: the short limit applies again.
	q := newPeer(t)
	r2 := newRig()
	r2.Found = func() bool { return false }
	r2.advance(0)
	r2.peers = []string{q.addr()}
	r2.advance(SearchTimeout)
	if !reflect.DeepEqual(q.got(), disconnect) {
		t.Fatalf("peer got %v", q.got())
	}
}

func TestNotesTellWhatIsGoingOn(t *testing.T) {
	for _, c := range []struct {
		answer string
		err    error
		want   string
	}{
		{`200 {"success":true}`, nil, "desk:5000 released the speakers"},
		{`501 {"success":false}`, nil, "desk:5000 did not release the speakers"},
		{"", errors.New("connection refused"), "desk:5000 did not answer"},
	} {
		r := newRig("desk:5000")
		var notes []string
		r.Note = func(s string) { notes = append(notes, s) }
		r.Post = func(context.Context, string) (string, error) { return c.answer, c.err }
		r.advance(0)
		r.advance(SearchTimeout)
		if want := []string{"Asking desk:5000 to release the speakers", c.want}; !reflect.DeepEqual(notes, want) {
			t.Errorf("got %q, want %q", notes, want)
		}
	}
}

// An instance that let go of the speakers asks nobody; told to reconnect, it
// starts a new session and at once asks again, also peers asked before.
func TestReleasedAsksNobodyAndStartsAfresh(t *testing.T) {
	p := newPeer(t)
	r := newRig(p.addr())
	released := false
	r.Released = func() bool { return released }
	r.advance(0)
	if !reflect.DeepEqual(p.got(), disconnect) {
		t.Fatalf("peer not asked: %v", p.got())
	}
	released = true
	r.advance(10 * SearchTimeout)
	if len(p.got()) != 1 {
		t.Fatalf("asked while released: %v", p.got())
	}
	released = false
	r.advance(time.Second) // the new session begins
	if len(p.got()) != 2 {
		t.Fatalf("not asked again at once after reconnecting: %v", p.got())
	}
}

// Several peers make two notes in all: what is being done, and the outcome.
func TestNotesSumUpARound(t *testing.T) {
	for _, c := range []struct {
		answers map[string]string // "" = no answer
		want    string
	}{
		{map[string]string{"a:5000": "200 ok", "b:5000": "", "c:5000": "200 ok"}, "a:5000 and c:5000 released the speakers"},
		{map[string]string{"a:5000": "501 no", "b:5000": "", "c:5000": ""}, "a:5000 did not release the speakers"},
		{map[string]string{"a:5000": "", "b:5000": "", "c:5000": ""}, "No other device answered"},
	} {
		r := newRig("a:5000", "b:5000", "c:5000")
		var notes []string
		r.Note = func(s string) { notes = append(notes, s) }
		r.Post = func(_ context.Context, peer string) (string, error) {
			if a := c.answers[peer]; a != "" {
				return a, nil
			}
			return "", errors.New("connection refused")
		}
		r.advance(0)
		if want := []string{"Asking 3 other devices to release the speakers", c.want}; !reflect.DeepEqual(notes, want) {
			t.Errorf("got %q, want %q", notes, want)
		}
	}
	// This computer itself is not counted, and with nobody else nothing is said.
	r := newRig("me:5000", "a:5000")
	r.IsSelf = func(p string) bool { return p == "me:5000" }
	var notes []string
	r.Note = func(s string) { notes = append(notes, s) }
	r.Post = func(context.Context, string) (string, error) { return "200 ok", nil }
	r.advance(0)
	if want := []string{"Asking a:5000 to release the speakers", "a:5000 released the speakers"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("got %q", notes)
	}
	r = newRig("me:5000")
	r.IsSelf = func(string) bool { return true }
	notes = nil
	r.Note = func(s string) { notes = append(notes, s) }
	r.advance(0)
	if len(notes) != 0 {
		t.Errorf("notes with nobody to ask: %q", notes)
	}
}

// Told to ask again, the user having pressed the key, an instance at once
// asks every peer once more, also those asked before; once is enough.
func TestAgainAsksEveryPeerOnceMore(t *testing.T) {
	a, b := newPeer(t), newPeer(t)
	r := newRig(a.addr(), b.addr())
	again := make(chan struct{}, 1)
	r.Again = again
	r.advance(0)
	r.advance(10 * SearchTimeout)
	if len(a.got()) != 1 || len(b.got()) != 1 {
		t.Fatalf("first round: %v %v", a.got(), b.got())
	}
	again <- struct{}{}
	r.advance(0)
	if len(a.got()) != 2 || len(b.got()) != 2 {
		t.Fatalf("not asked again: %v %v", a.got(), b.got())
	}
	r.advance(10 * SearchTimeout)
	if len(a.got()) != 2 || len(b.got()) != 2 {
		t.Fatalf("asked more than once more: %v %v", a.got(), b.got())
	}
}

// Connected, an instance has nobody to ask, and a press does not linger
// until the speakers are lost.
func TestAgainWhileConnectedAsksNobody(t *testing.T) {
	p := newPeer(t)
	r := newRig(p.addr())
	again := make(chan struct{}, 1)
	r.Again = again
	r.advance(0)
	r.connected = true
	again <- struct{}{}
	r.advance(time.Second)
	r.connected = false
	r.advance(time.Second)
	if len(p.got()) != 1 {
		t.Fatalf("asked: %v", p.got())
	}
}
