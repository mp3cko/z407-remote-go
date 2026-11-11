package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/logx"
	"github.com/mp3cko/z407-remote-go/internal/peers"
)

func (a *app) request(method, path string) (int, map[string]any) {
	a.t.Helper()
	req, _ := http.NewRequest(method, "http://"+a.addr+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var m map[string]any
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &m); err != nil {
		a.t.Fatalf("%s %s: %q is not JSON", method, path, body)
	}
	return resp.StatusCode, m
}

func (a *app) peers() ([]string, bool) {
	a.t.Helper()
	_, m := a.request("GET", "/api/peers")
	list := []string{}
	for _, v := range m["peers"].([]any) {
		list = append(list, v.(string))
	}
	return list, m["fixed"].(bool)
}

func configIn(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

func TestShutdownRequestStopsCleanly(t *testing.T) {
	fb := newFakeBackend()
	o := &fakeOpener{proc: newFakeProc()}
	a := startApp(t, deps{sp: fb, win: o})
	code, m := a.request("POST", "/api/shutdown")
	if code != 200 || m["success"] != true {
		t.Fatalf("answer: %d %v", code, m)
	}
	a.waitExit()
	// The same path as closing the window: the speaker link is released
	// (Run returns) and the window goes away.
	select {
	case <-fb.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the speaker connection was not shut down")
	}
	select {
	case <-o.proc.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the window was left open")
	}
	if !strings.Contains(a.out.String(), "Shutdown requested") {
		t.Errorf("output: %s", a.out.String())
	}
}

func TestPeersSavedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	a := startApp(t, deps{configDir: configIn(dir)})
	if list, fixed := a.peers(); len(list) != 0 || fixed {
		t.Fatalf("first start: %v fixed=%v", list, fixed)
	}
	if code, m := a.request("POST", "/api/peers?list=desk,192.168.1.20:5001"); code != 200 || m["success"] != true {
		t.Fatalf("save: %d %v", code, m)
	}
	if code, m := a.request("POST", "/api/peers?list=http://nope"); code != 400 || !strings.Contains(m["error"].(string), "not a valid address") {
		t.Fatalf("invalid save: %d %v", code, m)
	}
	a.cancel()
	a.waitExit()

	if _, err := os.Stat(filepath.Join(dir, "z407", "config.json")); err != nil {
		t.Fatalf("config file: %v", err)
	}
	b := startApp(t, deps{configDir: configIn(dir)})
	if list, _ := b.peers(); !reflect.DeepEqual(list, []string{"desk:5000", "192.168.1.20:5001"}) {
		t.Fatalf("after restart: %v", list)
	}
}

func TestPeersFlagOverridesWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	a := startApp(t, deps{configDir: configIn(dir)})
	a.request("POST", "/api/peers?list=saved-pc")
	a.cancel()
	a.waitExit()
	file := filepath.Join(dir, "z407", "config.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	b := startApp(t, deps{configDir: configIn(dir)}, "--peers", "flag-pc:6000,other")
	if list, fixed := b.peers(); !reflect.DeepEqual(list, []string{"flag-pc:6000", "other:5000"}) || !fixed {
		t.Fatalf("with flag: %v fixed=%v", list, fixed)
	}
	if code, _ := b.request("POST", "/api/peers?list=from-ui"); code != 409 {
		t.Fatalf("saving while the flag is set: %d", code)
	}
	b.cancel()
	b.waitExit()
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Fatalf("--peers changed the config file: %s", after)
	}

	c := startApp(t, deps{configDir: configIn(dir)})
	if list, fixed := c.peers(); !reflect.DeepEqual(list, []string{"saved-pc:5000"}) || fixed {
		t.Fatalf("next run without the flag: %v fixed=%v", list, fixed)
	}
}

func TestPeersFlagInvalid(t *testing.T) {
	d := deps{sp: newFakeBackend(), dial: dialerWith(nil, io.EOF)}
	err := runWith(context.Background(), []string{"--peers", "pc,http://x"}, io.Discard, d)
	if err == nil || !strings.Contains(err.Error(), "--peers") || !strings.Contains(err.Error(), "not a valid address") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigProblemsDoNotStopTheApp(t *testing.T) {
	a := startApp(t, deps{configDir: func() (string, error) { return "", io.ErrUnexpectedEOF }})
	if !strings.Contains(a.out.String(), "will not be saved") {
		t.Errorf("output: %s", a.out.String())
	}
	if code, _ := a.request("POST", "/api/peers?list=pc"); code != 200 {
		t.Errorf("in-memory save: %d", code)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "z407"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z407", "config.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := startApp(t, deps{configDir: configIn(dir)})
	if !strings.Contains(b.out.String(), "Could not read the list") {
		t.Errorf("output: %s", b.out.String())
	}
}

// searchingBackend never finds the speakers.
type searchingBackend struct {
	*fakeBackend
	connected atomic.Bool
}

func (s *searchingBackend) Status() (bool, int) { return s.connected.Load(), 50 }

func TestTakeoverAsksPeersWhenSpeakersAreNotFound(t *testing.T) {
	var mu sync.Mutex
	var hits []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.Method+" "+r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer peer.Close()
	peerAddr := strings.TrimPrefix(peer.URL, "http://")

	d := deps{
		sp:            &searchingBackend{fakeBackend: newFakeBackend()},
		takeover:      peers.Takeover{Timeout: 30 * time.Millisecond},
		takeoverEvery: 5 * time.Millisecond,
	}
	a := startApp(t, d, "--peers", peerAddr)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(hits)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer never asked; output:\n%s", a.out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.stillRunning(100 * time.Millisecond) // it keeps searching
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(hits, []string{"POST /api/shutdown"}) {
		t.Fatalf("peer got %v", hits)
	}
	if !strings.Contains(a.out.String(), "asked "+peerAddr+" to release them") {
		t.Errorf("output: %s", a.out.String())
	}
}

func TestTakeoverNeverAsksItself(t *testing.T) {
	// Listing our own address must not make the app shut itself down. The
	// port has to be known before the start, so pick a free one first.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()

	out := &safeBuf{}
	addrCh := make(chan net.Addr, 1)
	d := deps{
		sp:            &searchingBackend{fakeBackend: newFakeBackend()},
		takeover:      peers.Takeover{Timeout: 20 * time.Millisecond},
		takeoverEvery: 5 * time.Millisecond,
		dial:          dialerWith(nil, io.EOF),
		lookup:        func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil },
		log:           logx.NewWriter(out, false),
		ready:         func(addr net.Addr) { addrCh <- addr },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{t: t, out: out, cancel: cancel, errCh: make(chan error, 1)}
	go func() {
		a.errCh <- runWith(ctx, []string{"-ip", "127.0.0.1", "-port", port, "--peers", "127.0.0.1:" + port + ",my-own-name:" + port}, out, d)
	}()
	select {
	case addr := <-addrCh:
		a.addr = addr.String()
	case err := <-a.errCh:
		t.Skipf("port %s was taken in the meantime: %v", port, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(out.String(), "is this computer") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("own addresses not recognised; output:\n%s", out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.stillRunning(100 * time.Millisecond)
	if strings.Contains(out.String(), "Shutdown requested") {
		t.Fatalf("the app asked itself to shut down:\n%s", out.String())
	}
}

func TestSystemHelpers(t *testing.T) {
	if ips := systemIPs(); len(ips) == 0 {
		t.Error("no local addresses at all")
	}
	if ips, err := lookupHost("localhost"); err != nil || len(ips) == 0 || !ips[0].IsLoopback() {
		t.Errorf("localhost: %v, %v", ips, err)
	}
	if _, err := lookupHost("no-such-host.invalid"); err == nil {
		t.Error("expected a lookup error")
	}
	var d deps
	if d.ips() != nil {
		t.Error("deps without localIPs should have none")
	}
	if _, err := d.resolve("x"); err == nil {
		t.Error("deps without lookup should not resolve")
	}
}
