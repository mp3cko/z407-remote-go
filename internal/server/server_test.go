package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mp3cko/z407-remote-go/internal/logx"
)

type fakeSpeaker struct {
	mu        sync.Mutex
	connected bool
	volume    int
	// Progress
	connecting bool
	problem    string
	calls      []string
	err        error
}

func (f *fakeSpeaker) rec(n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, n)
	return f.err
}
func (f *fakeSpeaker) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
func (f *fakeSpeaker) Status() (bool, int) { return f.connected, f.volume }
func (f *fakeSpeaker) Progress() (bool, string) {
	return f.connecting, f.problem
}
func (f *fakeSpeaker) VolumeUp() error       { return f.rec("VolumeUp") }
func (f *fakeSpeaker) VolumeDown() error     { return f.rec("VolumeDown") }
func (f *fakeSpeaker) PlayPause() error      { return f.rec("PlayPause") }
func (f *fakeSpeaker) InputBluetooth() error { return f.rec("InputBluetooth") }
func (f *fakeSpeaker) InputAux() error       { return f.rec("InputAux") }
func (f *fakeSpeaker) InputUSB() error       { return f.rec("InputUSB") }
func (f *fakeSpeaker) BluetoothPair() error  { return f.rec("BluetoothPair") }
func (f *fakeSpeaker) FactoryReset() error   { return f.rec("FactoryReset") }
func (f *fakeSpeaker) BassUp() error         { return f.rec("BassUp") }
func (f *fakeSpeaker) BassDown() error       { return f.rec("BassDown") }
func (f *fakeSpeaker) NextTrack() error      { return f.rec("NextTrack") }
func (f *fakeSpeaker) PrevTrack() error      { return f.rec("PrevTrack") }

var quiet = WithLogger(logx.NewWriter(io.Discard, false))

// command -> speaker method.
var cases = []struct {
	cmd    string
	method string
}{
	{"vol_up", "VolumeUp"},
	{"vol_down", "VolumeDown"},
	{"play_pause", "PlayPause"},
	{"input_aux", "InputAux"},
	{"input_bluetooth", "InputBluetooth"},
	{"bluetooth_pair", "BluetoothPair"},
	{"factory_reset", "FactoryReset"},
	{"input_usb", "InputUSB"},
	{"bass_up", "BassUp"},
	{"bass_down", "BassDown"},
	{"next_speaker", "NextTrack"},
	{"prev_speaker", "PrevTrack"},
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCommands(t *testing.T) {
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			sp := &fakeSpeaker{connected: true}
			rec := do(New(sp, quiet), "POST", "/api/"+c.cmd)
			if rec.Code != 200 || decode(t, rec)["success"] != true {
				t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
			}
			if g := sp.got(); len(g) != 1 || g[0] != c.method {
				t.Fatalf("calls=%v", g)
			}
		})
	}
}

func TestNotConnected(t *testing.T) {
	names := []string{"nonsense"}
	for _, c := range cases {
		names = append(names, c.cmd)
	}
	for _, n := range names {
		sp := &fakeSpeaker{}
		rec := do(New(sp, quiet), "POST", "/api/"+n)
		m := decode(t, rec)
		if rec.Code != 404 || m["success"] != false ||
			m["error"] != "Speakers not connected. Waiting for background connection..." {
			t.Fatalf("%s: code=%d body=%s", n, rec.Code, rec.Body)
		}
		if len(sp.got()) != 0 {
			t.Fatalf("%s: side effects while disconnected", n)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	rec := do(New(&fakeSpeaker{connected: true}, quiet), "POST", "/api/bogus")
	m := decode(t, rec)
	if rec.Code != 400 || m["success"] != false || m["error"] != "Unknown command" {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestSpeakerError500(t *testing.T) {
	sp := &fakeSpeaker{connected: true, err: errors.New("boom")}
	rec := do(New(sp, quiet), "POST", "/api/vol_up")
	m := decode(t, rec)
	if rec.Code != 500 || m["success"] != false || m["error"] != "boom" {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := New(&fakeSpeaker{connected: true}, quiet)
	for _, m := range []string{"GET", "PUT", "DELETE"} {
		if rec := do(h, m, "/api/vol_up"); rec.Code != 405 {
			t.Fatalf("%s: code=%d", m, rec.Code)
		}
	}
	if rec := do(h, "POST", "/"); rec.Code != 405 {
		t.Fatalf("POST /: code=%d", rec.Code)
	}
}

func TestNotFound(t *testing.T) {
	h := New(&fakeSpeaker{connected: true}, quiet)
	for _, p := range []string{"/nope", "/api/", "/api/a/b", "/index.html"} {
		if rec := do(h, "GET", p); rec.Code != 404 {
			t.Fatalf("%s: code=%d", p, rec.Code)
		}
	}
}

func TestIndexByteIdentical(t *testing.T) {
	want, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	rec := do(New(&fakeSpeaker{}, quiet), "GET", "/")
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("code=%d, body differs from index.html", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("content-type=%q", ct)
	}
}

func TestStatus(t *testing.T) {
	for _, sp := range []*fakeSpeaker{{connected: true, volume: 55}, {}} {
		rec := do(New(sp, quiet), "GET", "/api/status")
		m := decode(t, rec)
		if rec.Code != 200 || len(m) != 4 || m["connecting"] != false || m["problem"] != "" || m["connected"] != sp.connected || m["volume"] != float64(sp.volume) {
			t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
		}
	}
}

func TestConcurrent(t *testing.T) {
	sp := &fakeSpeaker{connected: true}
	h := New(sp, quiet)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := cases[i%len(cases)]
			if rec := do(h, "POST", "/api/"+c.cmd); rec.Code != 200 {
				t.Errorf("%s: %d", c.cmd, rec.Code)
			}
			do(h, "GET", "/api/status")
		}(i)
	}
	wg.Wait()
	if len(sp.got()) != 100 {
		t.Fatalf("total calls = %d", len(sp.got()))
	}
}

func TestDefaultLogger(t *testing.T) {
	// Without WithLogger, the default logger is used and must not panic.
	h := New(&fakeSpeaker{connected: true, err: errors.New("x")})
	if rec := do(h, "POST", "/api/vol_up"); rec.Code != 500 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestAccessLog(t *testing.T) {
	var buf bytes.Buffer
	h := New(&fakeSpeaker{connected: true}, WithLogger(logx.NewWriter(&buf, true)))
	for _, c := range []struct{ m, p string }{{"POST", "/api/vol_up"}, {"POST", "/api/nope"}, {"GET", "/api/status"}} {
		req := httptest.NewRequest(c.m, c.p, nil)
		req.RemoteAddr = "192.168.1.50:4321"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	out := buf.String()
	if !strings.Contains(out, "POST /api/vol_up -> 200") || !strings.Contains(out, "from 192.168.1.50") {
		t.Errorf("missing success log: %q", out)
	}
	if !strings.Contains(out, "POST /api/nope -> 400") {
		t.Errorf("missing 400 log: %q", out)
	}
	if strings.Contains(out, "/api/status") {
		t.Errorf("status poll should not be logged: %q", out)
	}
}

func TestAddrs(t *testing.T) {
	if rec := do(New(&fakeSpeaker{}, quiet), "GET", "/api/addrs"); rec.Code != 200 || rec.Body.String() != `{"urls":[]}`+"\n" {
		t.Errorf("no addresses: %d %s", rec.Code, rec.Body)
	}
	h := New(&fakeSpeaker{}, quiet, WithAddrs(func() []string { return []string{"http://192.168.1.20:5000", "http://10.0.0.2:5000"} }))
	if rec := do(h, "GET", "/api/addrs"); rec.Code != 200 || rec.Body.String() != `{"urls":["http://192.168.1.20:5000","http://10.0.0.2:5000"]}`+"\n" {
		t.Errorf("addresses: %d %s", rec.Code, rec.Body)
	}
}

func TestStatusHidesStaleProgressOnceConnected(t *testing.T) {
	sp := &fakeSpeaker{connected: true, volume: 50, connecting: true, problem: "Speakers not found"}
	m := decode(t, do(New(sp, quiet), "GET", "/api/status"))
	if m["connecting"] != false || m["problem"] != "" {
		t.Fatalf("got %v", m)
	}
}
