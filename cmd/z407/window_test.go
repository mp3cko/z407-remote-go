package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/appwin"
	"github.com/mp3cko/z407-remote-go/internal/logx"
)

// fakeProc is a browser process that runs until exit is called.
type fakeProc struct {
	once   sync.Once
	done   chan struct{}
	err    error
	closed chan struct{}
}

func newFakeProc() *fakeProc {
	return &fakeProc{done: make(chan struct{}), closed: make(chan struct{})}
}
func (p *fakeProc) exit(err error) { p.once.Do(func() { p.err = err; close(p.done) }) }
func (p *fakeProc) Wait() error    { <-p.done; return p.err }
func (p *fakeProc) Close() error {
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
	p.exit(errors.New("signal: terminated"))
	return nil
}

type fakeOpener struct {
	mu         sync.Mutex
	proc       *fakeProc
	appErr     error
	defaultErr error
	apps       []string // URLs given to OpenApp
	ports      []int
	tabs       []string // URLs given to OpenDefault
}

func (o *fakeOpener) OpenApp(url string, port int) (appwin.Process, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.apps = append(o.apps, url)
	o.ports = append(o.ports, port)
	if o.appErr != nil {
		return nil, o.appErr
	}
	return o.proc, nil
}

func (o *fakeOpener) OpenDefault(url string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.tabs = append(o.tabs, url)
	return o.defaultErr
}

func (o *fakeOpener) opened() (apps, tabs []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.apps...), append([]string(nil), o.tabs...)
}

// app is a runWith under test.
type app struct {
	t      *testing.T
	addr   string
	out    *safeBuf
	cancel context.CancelFunc
	errCh  chan error
}

func startApp(t *testing.T, d deps, args ...string) *app {
	t.Helper()
	a := &app{t: t, out: &safeBuf{}, errCh: make(chan error, 1)}
	addrCh := make(chan net.Addr, 1)
	if d.sp == nil {
		d.sp = newFakeBackend()
	}
	d.dial = dialerWith(&net.UDPAddr{IP: net.ParseIP("10.1.2.3")}, nil)
	d.ready = func(addr net.Addr) { addrCh <- addr }
	if d.log == nil {
		d.log = logx.NewWriter(a.out, false)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	t.Cleanup(cancel)
	go func() { a.errCh <- runWith(ctx, append([]string{"-ip", "127.0.0.1", "-port", "0"}, args...), a.out, d) }()
	select {
	case addr := <-addrCh:
		a.addr = addr.String()
	case err := <-a.errCh:
		t.Fatalf("app did not start: %v", err)
	}
	return a
}

// waitExit fails unless the app stops cleanly soon.
func (a *app) waitExit() {
	a.t.Helper()
	select {
	case err := <-a.errCh:
		if err != nil {
			a.t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		a.t.Fatalf("app did not stop; output:\n%s", a.out.String())
	}
	if !strings.Contains(a.out.String(), "Goodbye") {
		a.t.Errorf("no clean shutdown in output:\n%s", a.out.String())
	}
}

// stillRunning fails if the app stops within d.
func (a *app) stillRunning(d time.Duration) {
	a.t.Helper()
	select {
	case err := <-a.errCh:
		a.t.Fatalf("app stopped (err=%v); output:\n%s", err, a.out.String())
	case <-time.After(d):
	}
	resp, err := http.Get("http://" + a.addr + "/api/status")
	if err != nil {
		a.t.Fatalf("app no longer answers: %v", err)
	}
	resp.Body.Close()
}

// page opens the event stream a browser page holds and returns its closer.
func (a *app) page() (closePage func()) {
	a.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+a.addr+"/api/events", nil)
	resp, err := (&http.Client{Transport: &http.Transport{}}).Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	if _, err := resp.Body.Read(make([]byte, 16)); err != nil {
		a.t.Fatal(err)
	}
	return func() { cancel(); resp.Body.Close() }
}

func TestWindowOpensAndClosingItStops(t *testing.T) {
	o := &fakeOpener{proc: newFakeProc()}
	a := startApp(t, deps{win: o})
	a.stillRunning(50 * time.Millisecond)

	apps, tabs := o.opened()
	_, port, _ := net.SplitHostPort(a.addr)
	if len(apps) != 1 || apps[0] != "http://127.0.0.1:"+port || len(tabs) != 0 {
		t.Fatalf("opened apps=%v tabs=%v", apps, tabs)
	}
	if got := o.ports[0]; port != strconv.Itoa(got) {
		t.Errorf("port %d passed, listening on %s", got, port)
	}

	o.proc.exit(nil) // the user closes the window
	a.waitExit()
	if !strings.Contains(a.out.String(), "Window closed") {
		t.Errorf("output: %s", a.out.String())
	}
	select {
	case <-o.proc.closed:
		t.Error("a window that is already closed must not be closed again")
	default:
	}
}

// A browser that exits with an error long after it started was still a
// window being closed (Chromium does not always exit with 0).
func TestWindowClosedWithErrorExitStillStops(t *testing.T) {
	o := &fakeOpener{proc: newFakeProc()}
	a := startApp(t, deps{win: o, earlyExit: time.Nanosecond})
	time.Sleep(5 * time.Millisecond)
	o.proc.exit(errors.New("exit status 1"))
	a.waitExit()
	if _, tabs := o.opened(); len(tabs) != 0 {
		t.Errorf("fell back to a tab: %v", tabs)
	}
}

func TestInterruptClosesWindow(t *testing.T) {
	o := &fakeOpener{proc: newFakeProc()}
	a := startApp(t, deps{win: o})
	a.cancel() // Ctrl+C
	a.waitExit()
	select {
	case <-o.proc.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the window was left open")
	}
}

func TestNoWindowFlag(t *testing.T) {
	o := &fakeOpener{proc: newFakeProc()}
	a := startApp(t, deps{win: o, grace: 10 * time.Millisecond}, "--no-window")
	// Headless: pages come and go without stopping the app.
	a.page()()
	a.stillRunning(100 * time.Millisecond)
	if apps, tabs := o.opened(); len(apps)+len(tabs) != 0 {
		t.Fatalf("opened apps=%v tabs=%v", apps, tabs)
	}
	a.cancel()
	a.waitExit()
}

func TestFallbackTabLifetime(t *testing.T) {
	o := &fakeOpener{appErr: appwin.ErrNoBrowser}
	a := startApp(t, deps{win: o, grace: 60 * time.Millisecond})
	_, port, _ := net.SplitHostPort(a.addr)
	if apps, tabs := o.opened(); len(apps) != 1 || len(tabs) != 1 || tabs[0] != "http://127.0.0.1:"+port {
		t.Fatalf("opened apps=%v tabs=%v", apps, tabs)
	}
	// Nothing happens while the browser has not loaded the page yet.
	a.stillRunning(150 * time.Millisecond)

	closeOld := a.page()
	a.stillRunning(150 * time.Millisecond)

	// Reload: the old page's stream closes, the new one opens right after.
	closeOld()
	closeNew := a.page()
	a.stillRunning(200 * time.Millisecond)

	closeNew() // the tab is closed
	a.waitExit()
	if !strings.Contains(a.out.String(), "page was closed") {
		t.Errorf("output: %s", a.out.String())
	}
}

func TestFallbackInterrupt(t *testing.T) {
	o := &fakeOpener{appErr: appwin.ErrNoBrowser}
	a := startApp(t, deps{win: o})
	closePage := a.page()
	defer closePage()
	a.cancel() // Ctrl+C with a page still open must not hang on its stream
	a.waitExit()
}

func TestWindowThatFailsAtOnceFallsBackToTab(t *testing.T) {
	o := &fakeOpener{proc: newFakeProc()}
	o.proc.exit(errors.New("exit status 1"))
	a := startApp(t, deps{win: o, grace: 30 * time.Millisecond})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, tabs := o.opened(); len(tabs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no fallback tab; output:\n%s", a.out.String())
		}
		time.Sleep(time.Millisecond)
	}
	a.stillRunning(50 * time.Millisecond)
	a.page()()
	a.waitExit()
}

func TestWindowStartErrorFallsBackToTab(t *testing.T) {
	o := &fakeOpener{appErr: errors.New("exec format error")}
	a := startApp(t, deps{win: o})
	if _, tabs := o.opened(); len(tabs) != 1 {
		t.Fatalf("tabs=%v", tabs)
	}
	if !strings.Contains(a.out.String(), "exec format error") {
		t.Errorf("error not logged: %s", a.out.String())
	}
}

func TestNoDisplayRunsHeadless(t *testing.T) {
	o := &fakeOpener{appErr: appwin.ErrNoDisplay}
	a := startApp(t, deps{win: o, grace: 10 * time.Millisecond})
	a.page()()
	a.stillRunning(100 * time.Millisecond)
	if _, tabs := o.opened(); len(tabs) != 0 {
		t.Fatalf("tabs=%v", tabs)
	}
	if !strings.Contains(a.out.String(), "No display") {
		t.Errorf("output: %s", a.out.String())
	}
}

func TestNoBrowserAtAllRunsHeadless(t *testing.T) {
	o := &fakeOpener{appErr: appwin.ErrNoBrowser, defaultErr: errors.New("xdg-open: not found")}
	a := startApp(t, deps{win: o, grace: 10 * time.Millisecond})
	a.page()() // the user opened and closed the page by hand
	a.stillRunning(100 * time.Millisecond)
	if !strings.Contains(a.out.String(), "Open http://127.0.0.1:") {
		t.Errorf("output: %s", a.out.String())
	}
}

func TestLocalURL(t *testing.T) {
	for _, c := range []struct {
		ip   string
		want string
	}{
		{"0.0.0.0", "http://127.0.0.1:5000"},
		{"", "http://127.0.0.1:5000"},
		{"::", "http://127.0.0.1:5000"},
		{"192.168.1.7", "http://192.168.1.7:5000"},
		{"::1", "http://[::1]:5000"},
		{"localhost", "http://localhost:5000"},
	} {
		if got := localURL(c.ip, 5000); got != c.want {
			t.Errorf("%q: %s", c.ip, got)
		}
	}
}

func TestLogFile(t *testing.T) {
	if got, want := logFilePath("base", "tmp"), filepath.Join("base", "z407", "z407.log"); got != want {
		t.Errorf("got %s", got)
	}
	if got, want := logFilePath("", "tmp"), filepath.Join("tmp", "z407", "z407.log"); got != want {
		t.Errorf("fallback: got %s", got)
	}

	path := logFilePath(t.TempDir(), "")
	write := func(s string) {
		t.Helper()
		f, err := openLogFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, s); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	write("one\n")
	write("two\n")
	if b, _ := os.ReadFile(path); string(b) != "one\ntwo\n" {
		t.Errorf("log is not appended to: %q", b)
	}
	write(strings.Repeat("x", maxLogSize))
	write("fresh\n")
	if b, _ := os.ReadFile(path); string(b) != "fresh\n" {
		t.Errorf("oversized log not restarted: %d bytes", len(b))
	}
	if _, err := openLogFile(filepath.Join(path, "below-a-file", "z407.log")); err == nil {
		t.Error("expected an error for an impossible directory")
	}
}
