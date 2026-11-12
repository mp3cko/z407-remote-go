package ble

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/protocol"
	"github.com/mp3cko/z407-remote-go/internal/speaker"
)

type fakeChar struct {
	disabled  int
	notifyErr error
	writeErr  error
	cb        func([]byte)
	written   [][]byte
}

func (c *fakeChar) enableNotifications(cb func([]byte)) error { c.cb = cb; return c.notifyErr }
func (c *fakeChar) disableNotifications() error               { c.disabled++; return errors.New("ignored") }
func (c *fakeChar) writeWithoutResponse(p []byte) error {
	c.written = append(c.written, p)
	return c.writeErr
}

type fakeDev struct {
	connHook     func()
	inConnected  atomic.Bool
	overlap      atomic.Bool
	closedFlag   atomic.Bool
	afterClose   atomic.Bool
	chars        map[string]characteristic
	charsErr     error
	disconnected int
	mu           sync.Mutex
	gone         bool
	connErr      error
}

func (d *fakeDev) characteristics(string, []string) (map[string]characteristic, error) {
	return d.chars, d.charsErr
}
func (d *fakeDev) disconnect() error {
	if d.inConnected.Load() {
		d.overlap.Store(true)
	}
	d.disconnected++
	d.closedFlag.Store(true)
	return nil
}
func (d *fakeDev) connected() (bool, error) {
	if d.closedFlag.Load() {
		d.afterClose.Store(true)
	}
	if d.connHook != nil {
		d.inConnected.Store(true)
		defer d.inConnected.Store(false)
		d.connHook()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.gone, d.connErr
}
func (d *fakeDev) drop() { d.mu.Lock(); d.gone = true; d.mu.Unlock() }

type fakeRadio struct {
	enableErr  error
	results    []scanResult
	scanErr    error
	blockScan  bool // keep scanning until stopScan
	stop       chan struct{}
	stopped    int
	stopFails  int // stopScan fails this many times before succeeding
	connectErr error
	dev        *fakeDev
	onDisc     func()
	gotAddr    any
}

func newRadio() *fakeRadio { return &fakeRadio{stop: make(chan struct{}, 4), dev: &fakeDev{}} }

func (r *fakeRadio) enable() error { return r.enableErr }
func (r *fakeRadio) scan(cb func(scanResult)) error {
	for _, res := range r.results {
		cb(res)
	}
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.blockScan {
		<-r.stop
	}
	return nil
}
func (r *fakeRadio) stopScan() error {
	r.stopped++
	if r.stopped <= r.stopFails {
		return errors.New("not scanning yet")
	}
	select {
	case r.stop <- struct{}{}:
	default:
	}
	return nil
}
func (r *fakeRadio) connect(addr any, onDisc func()) (device, error) {
	r.gotAddr, r.onDisc = addr, onDisc
	if r.connectErr != nil {
		return nil, r.connectErr
	}
	return r.dev, nil
}

func goodDev() (*fakeDev, *fakeChar, *fakeChar) {
	cmd, resp := &fakeChar{}, &fakeChar{}
	return &fakeDev{chars: map[string]characteristic{protocol.CommandUUID: cmd, protocol.ResponseUUID: resp}}, cmd, resp
}

func dialer(r *fakeRadio) *Dialer {
	return &Dialer{r: r, scanTimeout: 50 * time.Millisecond, pollInterval: 5 * time.Millisecond, stopRetry: time.Millisecond}
}

func TestDialSuccess(t *testing.T) {
	r := newRadio()
	var cmd, resp *fakeChar
	r.dev, cmd, resp = goodDev()
	r.results = []scanResult{{addr: "other"}, {addr: "z407", hasService: true}, {addr: "late", hasService: true}}
	r.blockScan = true

	var notified []byte
	disc, found := 0, 0
	l, err := dialer(r).Dial(context.Background(), speaker.Handler{
		OnFound:      func() { found++ },
		OnNotify:     func(b []byte) { notified = b },
		OnDisconnect: func() { disc++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if found != 1 {
		t.Errorf("OnFound called %d times", found)
	}
	if r.gotAddr != "z407" {
		t.Errorf("addr %v", r.gotAddr)
	}
	resp.cb([]byte{1, 2})
	if len(notified) != 2 {
		t.Errorf("notify %v", notified)
	}
	if err := l.Write(protocol.Handshake); err != nil || len(cmd.written) != 1 {
		t.Errorf("write %v %v", err, cmd.written)
	}
	r.onDisc()
	r.onDisc()
	if disc != 1 {
		t.Errorf("disc=%d", disc)
	}
	if err := l.Close(); err != nil || r.dev.disconnected != 1 {
		t.Errorf("close %v", err)
	}
}

func TestCloseSuppressesOnDisconnect(t *testing.T) {
	r := newRadio()
	r.dev, _, _ = goodDev()
	r.results = []scanResult{{hasService: true}}
	disc := 0
	l, err := dialer(r).Dial(context.Background(), speaker.Handler{OnDisconnect: func() { disc++ }})
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	r.onDisc()
	if disc != 0 {
		t.Error("OnDisconnect fired after Close")
	}
}

func TestNilHandlers(t *testing.T) {
	r := newRadio()
	var resp *fakeChar
	r.dev, _, resp = goodDev()
	r.results = []scanResult{{hasService: true}}
	if _, err := dialer(r).Dial(context.Background(), speaker.Handler{}); err != nil {
		t.Fatal(err)
	}
	resp.cb(nil)
	r.onDisc()
}

func TestEnableError(t *testing.T) {
	r := newRadio()
	r.enableErr = errors.New("no adapter")
	_, err := dialer(r).Dial(context.Background(), speaker.Handler{})
	if err == nil || !strings.Contains(err.Error(), "no adapter") || !errors.Is(err, speaker.ErrNoBluetooth) {
		t.Fatal(err)
	}
}

func TestNotFoundTimeout(t *testing.T) {
	r := newRadio()
	r.blockScan = true
	r.results = []scanResult{{addr: "x"}}
	found := false
	_, err := dialer(r).Dial(context.Background(), speaker.Handler{OnFound: func() { found = true }})
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, speaker.ErrNotFound) {
		t.Fatal(err)
	}
	if found {
		t.Error("OnFound called for speakers that were not found")
	}
	if r.stopped == 0 {
		t.Error("scan not stopped")
	}
}

func TestContextCanceled(t *testing.T) {
	r := newRadio()
	r.blockScan = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := dialer(r)
	d.scanTimeout = time.Minute
	if _, err := d.Dial(ctx, speaker.Handler{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestScanEndsWithoutMatch(t *testing.T) {
	r := newRadio()
	if _, err := dialer(r).Dial(context.Background(), speaker.Handler{}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestScanError(t *testing.T) {
	r := newRadio()
	r.scanErr = errors.New("scan boom")
	if _, err := dialer(r).Dial(context.Background(), speaker.Handler{}); err == nil || !strings.Contains(err.Error(), "scan boom") {
		t.Fatal(err)
	}
}

func TestScanErrorAfterMatch(t *testing.T) {
	r := newRadio()
	r.results = []scanResult{{hasService: true}}
	r.scanErr = errors.New("late")
	r.dev, _, _ = goodDev()
	if _, err := dialer(r).Dial(context.Background(), speaker.Handler{}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectFailureWindowsHint(t *testing.T) {
	r := newRadio()
	r.results = []scanResult{{hasService: true}}
	r.connectErr = errors.New("Catastrophic failure (2147418113)")
	_, err := dialer(r).Dial(context.Background(), speaker.Handler{})
	if err == nil || !strings.Contains(err.Error(), "re-pair") {
		t.Fatal(err)
	}
}

func TestConnectFailurePlain(t *testing.T) {
	r := newRadio()
	r.results = []scanResult{{hasService: true}}
	r.connectErr = errors.New("refused")
	_, err := dialer(r).Dial(context.Background(), speaker.Handler{})
	if err == nil || strings.Contains(err.Error(), "re-pair") || !strings.Contains(err.Error(), "refused") {
		t.Fatal(err)
	}
}

func TestDiscoverError(t *testing.T) {
	r := newRadio()
	r.results = []scanResult{{hasService: true}}
	r.dev.charsErr = errors.New("gatt")
	if _, err := dialer(r).Dial(context.Background(), speaker.Handler{}); err == nil || r.dev.disconnected != 1 {
		t.Fatal(err)
	}
}

func TestMissingCharacteristic(t *testing.T) {
	r := newRadio()
	r.results = []scanResult{{hasService: true}}
	r.dev.chars = map[string]characteristic{protocol.CommandUUID: &fakeChar{}}
	_, err := dialer(r).Dial(context.Background(), speaker.Handler{})
	if err == nil || !strings.Contains(err.Error(), "missing") || r.dev.disconnected != 1 {
		t.Fatal(err)
	}
}

func TestNotifyError(t *testing.T) {
	r := newRadio()
	r.results = []scanResult{{hasService: true}}
	d, _, resp := goodDev()
	resp.notifyErr = errors.New("cccd")
	r.dev = d
	_, err := dialer(r).Dial(context.Background(), speaker.Handler{})
	if err == nil || !strings.Contains(err.Error(), "cccd") || d.disconnected != 1 {
		t.Fatal(err)
	}
}

func TestNewDialer(t *testing.T) {
	if d := NewDialer(); d == nil || d.scanTimeout != defaultScanTimeout {
		t.Fatal("bad dialer")
	}
}

func TestStopScanRetriedUntilSuccess(t *testing.T) {
	r := newRadio()
	r.blockScan = true
	r.stopFails = 3
	if _, err := dialer(r).Dial(context.Background(), speaker.Handler{}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if r.stopped != 4 {
		t.Errorf("stopScan called %d times, want 4", r.stopped)
	}
}

func TestStopScanRetryEndsWhenScanEnds(t *testing.T) {
	r := newRadio()
	r.stopFails = 1 << 30 // never succeeds
	done := make(chan struct{})
	d := dialer(r)
	d.r = &endingRadio{fakeRadio: r, end: done}
	go func() { time.Sleep(20 * time.Millisecond); close(done) }()
	if _, err := d.Dial(context.Background(), speaker.Handler{}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

// endingRadio's scan returns when end is closed, regardless of stopScan.
type endingRadio struct {
	*fakeRadio
	end chan struct{}
}

func (r *endingRadio) scan(func(scanResult)) error { <-r.end; return nil }

func TestPollReportsRemoteDisconnect(t *testing.T) {
	r := newRadio()
	r.dev, _, _ = goodDev()
	r.results = []scanResult{{hasService: true}}
	disc := make(chan struct{}, 4)
	l, err := dialer(r).Dial(context.Background(), speaker.Handler{OnDisconnect: func() { disc <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	r.dev.drop()
	select {
	case <-disc:
	case <-time.After(time.Second):
		t.Fatal("no OnDisconnect after remote drop")
	}
	r.onDisc() // platform callback too: still only once
	time.Sleep(20 * time.Millisecond)
	if len(disc) != 0 {
		t.Error("OnDisconnect fired twice")
	}
}

func TestPollIgnoresErrorsAndStopsOnClose(t *testing.T) {
	r := newRadio()
	r.dev, _, _ = goodDev()
	r.dev.connErr = errors.New("transient")
	r.dev.gone = true // error takes precedence: not a disconnect
	r.results = []scanResult{{hasService: true}}
	disc := 0
	var mu sync.Mutex
	l, err := dialer(r).Dial(context.Background(), speaker.Handler{OnDisconnect: func() { mu.Lock(); disc++; mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	_ = l.Close()
	r.dev.mu.Lock()
	r.dev.connErr = nil
	r.dev.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if disc != 0 {
		t.Errorf("disc=%d", disc)
	}
}

func TestCloseDisablesNotifications(t *testing.T) {
	r := newRadio()
	var resp *fakeChar
	r.dev, _, resp = goodDev()
	r.results = []scanResult{{hasService: true}}
	l, err := dialer(r).Dial(context.Background(), speaker.Handler{})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("close should ignore disable error: %v", err)
	}
	if resp.disabled != 1 || r.dev.disconnected != 1 {
		t.Errorf("disabled=%d disconnected=%d", resp.disabled, r.dev.disconnected)
	}
}

func TestCloseWaitsForInFlightPoll(t *testing.T) {
	r := newRadio()
	r.dev, _, _ = goodDev()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	r.dev.connHook = func() {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}
	r.results = []scanResult{{hasService: true}}
	l, err := dialer(r).Dial(context.Background(), speaker.Handler{})
	if err != nil {
		t.Fatal(err)
	}
	<-entered // poll is now inside connected()
	closed := make(chan struct{})
	go func() { _ = l.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while connected() was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-closed
	if r.dev.overlap.Load() {
		t.Error("disconnect overlapped connected()")
	}
}

func TestNoPollAfterClose(t *testing.T) {
	r := newRadio()
	r.dev, _, _ = goodDev()
	r.results = []scanResult{{hasService: true}}
	// Deterministically force the race: the tick is taken, then Close runs
	// to completion before the poll reaches devMu.
	tick := make(chan struct{}, 1)
	resume := make(chan struct{})
	d := dialer(r)
	d.prePoll = func() {
		select {
		case tick <- struct{}{}:
		default:
		}
		<-resume
	}
	l, err := d.Dial(context.Background(), speaker.Handler{})
	if err != nil {
		t.Fatal(err)
	}
	<-tick
	_ = l.Close()
	close(resume)
	time.Sleep(30 * time.Millisecond)
	if r.dev.afterClose.Load() {
		t.Fatal("connected() called after disconnect")
	}
}
