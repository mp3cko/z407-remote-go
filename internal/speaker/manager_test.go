package speaker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/logx"
	"github.com/mp3cko/z407-remote-go/internal/protocol"
)

type fakeLink struct {
	mu       sync.Mutex
	writes   [][]byte
	writeErr error
	closed   int
	onWrite  func(cmd protocol.Command)
}

func (l *fakeLink) Write(cmd protocol.Command) error {
	l.mu.Lock()
	err := l.writeErr
	if err == nil {
		l.writes = append(l.writes, append([]byte(nil), cmd...))
	}
	cb := l.onWrite
	l.mu.Unlock()
	if cb != nil {
		cb(cmd)
	}
	return err
}

func (l *fakeLink) Close() error {
	l.mu.Lock()
	l.closed++
	l.mu.Unlock()
	return nil
}

func (l *fakeLink) written() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]byte(nil), l.writes...)
}

func (l *fakeLink) setWriteErr(err error) {
	l.mu.Lock()
	l.writeErr = err
	l.mu.Unlock()
}

func (l *fakeLink) closeCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

type fakeDialer struct {
	mu       sync.Mutex
	dials    int
	failN    int // first failN dials fail
	links    []*fakeLink
	handlers []Handler
	prepare  func(l *fakeLink, h Handler) // runs inside Dial
}

func (d *fakeDialer) Dial(ctx context.Context, h Handler) (Link, error) {
	d.mu.Lock()
	d.dials++
	if d.dials <= d.failN {
		d.mu.Unlock()
		return nil, errors.New("not found")
	}
	l := &fakeLink{}
	d.links = append(d.links, l)
	d.handlers = append(d.handlers, h)
	prep := d.prepare
	d.mu.Unlock()
	if prep != nil {
		prep(l, h)
	}
	return l, nil
}

func (d *fakeDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

func (d *fakeDialer) link(i int) *fakeLink {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i >= len(d.links) {
		return nil
	}
	return d.links[i]
}

func (d *fakeDialer) handler(i int) Handler {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.handlers[i]
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func start(t *testing.T, d Dialer) (*Manager, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	m := NewManager(d, WithRetryInterval(2*time.Millisecond), WithHeartbeat(5*time.Millisecond), WithLogger(func(logx.Level, string, ...any) {}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return m, cancel, done
}

func waitConnected(t *testing.T, m *Manager) {
	t.Helper()
	eventually(t, "connected", func() bool { c, _ := m.Status(); return c })
}

func TestHandshakeAfterDial(t *testing.T) {
	d := &fakeDialer{}
	m, _, _ := start(t, d)
	waitConnected(t, m)
	w := d.link(0).written()
	if len(w) != 1 || !bytes.Equal(w[0], []byte{0x84, 0x05}) {
		t.Fatalf("writes = %x", w)
	}
}

func TestNotConnected(t *testing.T) {
	m := NewManager(&fakeDialer{})
	if err := m.VolumeUp(); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
	if c, v := m.Status(); c || v != 50 {
		t.Fatalf("status = %v %d", c, v)
	}
}

func TestCommandBytes(t *testing.T) {
	d := &fakeDialer{}
	m, _, _ := start(t, d)
	waitConnected(t, m)
	cases := []struct {
		name string
		fn   func() error
		want string
	}{
		{"bassup", m.BassUp, "\x80\x00"},
		{"bassdown", m.BassDown, "\x80\x01"},
		{"volup", m.VolumeUp, "\x80\x02"},
		{"voldown", m.VolumeDown, "\x80\x03"},
		{"playpause", m.PlayPause, "\x80\x04"},
		{"next", m.NextTrack, "\x80\x05"},
		{"prev", m.PrevTrack, "\x80\x06"},
		{"bt", m.InputBluetooth, "\x81\x01"},
		{"aux", m.InputAux, "\x81\x02"},
		{"usb", m.InputUSB, "\x81\x03"},
		{"pair", m.BluetoothPair, "\x82\x00"},
		{"reset", m.FactoryReset, "\x83\x00"},
	}
	for i, c := range cases {
		if err := c.fn(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		w := d.link(0).written()
		if got := w[len(w)-1]; string(got) != c.want {
			t.Errorf("%s: got %x want %x", c.name, got, []byte(c.want))
		}
		if len(w) != i+2 {
			t.Errorf("%s: writes = %d", c.name, len(w))
		}
	}
}

func TestVolumeClamp(t *testing.T) {
	d := &fakeDialer{}
	m, _, _ := start(t, d)
	waitConnected(t, m)
	for i := 0; i < 15; i++ {
		_ = m.VolumeUp()
	}
	if _, v := m.Status(); v != 100 {
		t.Fatalf("vol = %d", v)
	}
	for i := 0; i < 30; i++ {
		_ = m.VolumeDown()
	}
	if _, v := m.Status(); v != 0 {
		t.Fatalf("vol = %d", v)
	}
	_ = m.VolumeUp()
	if _, v := m.Status(); v != 5 {
		t.Fatalf("vol = %d", v)
	}
}

func TestKeepAliveReplyAndReady(t *testing.T) {
	d := &fakeDialer{}
	m, _, _ := start(t, d)
	waitConnected(t, m)
	d.handler(0).OnNotify([]byte{0xd4, 0x05, 0x01})
	w := d.link(0).written()
	if last := w[len(w)-1]; !bytes.Equal(last, []byte{0x84, 0x00}) {
		t.Fatalf("last write %x", last)
	}
	before := len(w)
	d.handler(0).OnNotify([]byte{0xd4, 0x00, 0x01})
	d.handler(0).OnNotify([]byte{0x01})
	if len(d.link(0).written()) != before {
		t.Fatal("unexpected write")
	}
}

func TestKeepAliveDuringDialIsDeferred(t *testing.T) {
	d := &fakeDialer{prepare: func(l *fakeLink, h Handler) {
		h.OnNotify([]byte{0xd4, 0x05, 0x01}) // before Dial returns
	}}
	m, _, _ := start(t, d)
	waitConnected(t, m)
	w := d.link(0).written()
	if len(w) != 2 || !bytes.Equal(w[0], []byte{0x84, 0x05}) || !bytes.Equal(w[1], []byte{0x84, 0x00}) {
		t.Fatalf("writes = %x", w)
	}
}

func TestReadyNotifyMarksConnected(t *testing.T) {
	d := &fakeDialer{}
	m := NewManager(d, WithLogger(nil))
	// Not running: simulate by dialing manually via connectOnce.
	m.connectOnce(context.Background())
	d.handler(0).OnDisconnect()
	if c, _ := m.Status(); c {
		t.Fatal("should be disconnected")
	}
	d.handler(0).OnNotify([]byte{0xd4, 0x00, 0x01})
	if c, _ := m.Status(); !c {
		t.Fatal("ready notify should mark connected")
	}
}

func TestWriteErrorReconnects(t *testing.T) {
	d := &fakeDialer{}
	m, _, _ := start(t, d)
	waitConnected(t, m)
	d.link(0).setWriteErr(errors.New("boom"))
	if err := m.PlayPause(); err == nil || errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
	eventually(t, "redial", func() bool { return d.link(1) != nil })
	waitConnected(t, m)
	if d.link(0).closeCount() == 0 {
		t.Fatal("old link not closed")
	}
	if err := m.PlayPause(); err != nil {
		t.Fatal(err)
	}
}

func TestDialFailureRetries(t *testing.T) {
	d := &fakeDialer{failN: 3}
	m, _, _ := start(t, d)
	waitConnected(t, m)
	if n := d.dialCount(); n != 4 {
		t.Fatalf("dials = %d", n)
	}
}

func TestHandshakeFailureRetries(t *testing.T) {
	first := true
	var dm sync.Mutex
	d := &fakeDialer{}
	d.prepare = func(l *fakeLink, h Handler) {
		dm.Lock()
		defer dm.Unlock()
		if first {
			first = false
			l.writeErr = errors.New("nope")
		}
	}
	m, _, _ := start(t, d)
	waitConnected(t, m)
	if d.link(0).closeCount() == 0 {
		t.Fatal("failed link not closed")
	}
}

func TestDisconnectCallbackReconnects(t *testing.T) {
	d := &fakeDialer{}
	m, _, _ := start(t, d)
	waitConnected(t, m)
	d.handler(0).OnDisconnect()
	eventually(t, "redial", func() bool { return d.link(1) != nil })
	waitConnected(t, m)
	// stale callback from the old link must not affect the new connection
	d.handler(0).OnDisconnect()
	if c, _ := m.Status(); !c {
		t.Fatal("stale disconnect dropped new link")
	}
}

func TestDisconnectDuringDial(t *testing.T) {
	var once sync.Once
	d := &fakeDialer{prepare: func(l *fakeLink, h Handler) {
		once.Do(h.OnDisconnect)
	}}
	m, _, _ := start(t, d)
	eventually(t, "second dial", func() bool { return d.link(1) != nil })
	waitConnected(t, m)
}

func TestCancelClosesLink(t *testing.T) {
	d := &fakeDialer{}
	m, cancel, done := start(t, d)
	waitConnected(t, m)
	cancel()
	<-done
	if d.link(0).closeCount() == 0 {
		t.Fatal("link not closed")
	}
	if c, _ := m.Status(); c {
		t.Fatal("still connected")
	}
	if err := m.PlayPause(); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelDuringRetryWait(t *testing.T) {
	d := &fakeDialer{failN: 1 << 30}
	m := NewManager(d, WithRetryInterval(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	eventually(t, "dial", func() bool { return d.dialCount() >= 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestDefaultsAndOptionsIgnoreNonPositive(t *testing.T) {
	m := NewManager(&fakeDialer{}, WithRetryInterval(0), WithHeartbeat(-1))
	if m.retry != 5*time.Second || m.heartbeat != 5*time.Second {
		t.Fatalf("defaults: %v %v", m.retry, m.heartbeat)
	}
	m.logf(logx.Info, "noop %d", 1) // nil logger must be safe
	var got string
	m = NewManager(&fakeDialer{}, WithLogger(func(_ logx.Level, f string, a ...any) { got = f }))
	m.logf(logx.Info, "x")
	if got != "x" {
		t.Fatal("logger not used")
	}
}

func TestConcurrentUse(t *testing.T) {
	d := &fakeDialer{}
	m, _, _ := start(t, d)
	waitConnected(t, m)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				switch (i + j) % 4 {
				case 0:
					_ = m.VolumeUp()
				case 1:
					_ = m.VolumeDown()
				case 2:
					m.Status()
				default:
					if h := d.handler(0); j%10 == 0 {
						h.OnNotify([]byte{0xd4, 0x05, 0x01})
					}
					_ = m.PlayPause()
				}
			}
		}(i)
	}
	wg.Wait()
	if _, v := m.Status(); v < 0 || v > 100 {
		t.Fatalf("vol = %d", v)
	}
}

func TestRepeatedFailureLoggedOnce(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	d := &fakeDialer{failN: 5}
	m := NewManager(d, WithRetryInterval(time.Millisecond), WithHeartbeat(5*time.Millisecond),
		WithLogger(func(_ logx.Level, f string, a ...any) {
			mu.Lock()
			lines = append(lines, fmt.Sprintf(f, a...))
			mu.Unlock()
		}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	waitConnected(t, m)

	mu.Lock()
	defer mu.Unlock()
	fails := 0
	for _, l := range lines {
		if strings.Contains(l, "Dial failed") {
			fails++
		}
	}
	if fails != 1 {
		t.Errorf("identical failure logged %d times, want 1: %q", fails, lines)
	}
}
