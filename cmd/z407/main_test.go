package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *safeBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type fakeBackend struct {
	ran  chan struct{}
	done chan struct{}
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{ran: make(chan struct{}), done: make(chan struct{})}
}
func (f *fakeBackend) Run(ctx context.Context)    { close(f.ran); <-ctx.Done(); close(f.done) }
func (f *fakeBackend) Status() (bool, int)        { return true, 42 }
func (f *fakeBackend) Progress() (string, string) { return "", "" }
func (f *fakeBackend) Found() bool                { return false }
func (f *fakeBackend) VolumeUp() error            { return nil }
func (f *fakeBackend) VolumeDown() error          { return nil }
func (f *fakeBackend) PlayPause() error           { return nil }
func (f *fakeBackend) InputBluetooth() error      { return nil }
func (f *fakeBackend) InputAux() error            { return nil }
func (f *fakeBackend) InputUSB() error            { return nil }
func (f *fakeBackend) BluetoothPair() error       { return nil }
func (f *fakeBackend) FactoryReset() error        { return nil }
func (f *fakeBackend) BassUp() error              { return nil }
func (f *fakeBackend) BassDown() error            { return nil }
func (f *fakeBackend) NextTrack() error           { return nil }
func (f *fakeBackend) PrevTrack() error           { return nil }

type fakeConn struct {
	net.Conn
	addr net.Addr
}

func (c fakeConn) LocalAddr() net.Addr { return c.addr }
func (c fakeConn) Close() error        { return nil }

func dialerWith(addr net.Addr, err error) func(string, string) (net.Conn, error) {
	return func(string, string) (net.Conn, error) {
		if err != nil {
			return nil, err
		}
		return fakeConn{addr: addr}, nil
	}
}

func TestGetIP(t *testing.T) {
	if got := getIP(dialerWith(&net.UDPAddr{IP: net.ParseIP("192.168.1.7")}, nil)); got != "192.168.1.7" {
		t.Fatalf("got %q", got)
	}
	if got := getIP(dialerWith(nil, errors.New("no route"))); got != "127.0.0.1" {
		t.Fatalf("got %q", got)
	}
	if got := getIP(dialerWith(&net.TCPAddr{}, nil)); got != "127.0.0.1" {
		t.Fatalf("non-UDP addr: got %q", got)
	}
	if got := getIP(dialerWith(&net.UDPAddr{}, nil)); got != "127.0.0.1" {
		t.Fatalf("nil IP: got %q", got)
	}
}

func TestBanner(t *testing.T) {
	b := banner([]hostAddr{{"eth0", "10.0.0.2"}}, 5000, "0.0.0.0")
	for _, w := range []string{"REMOTE CONTROL AVAILABLE", "http://10.0.0.2:5000"} {
		if !strings.Contains(b, w) {
			t.Errorf("missing %q", w)
		}
	}
	b = banner([]hostAddr{{"eth0", "10.0.0.2"}}, 8080, "127.0.0.1")
	if !strings.Contains(b, "Bound specifically to: 127.0.0.1") || !strings.Contains(b, ":8080") {
		t.Errorf("bad bound banner: %s", b)
	}
}

func TestTexts(t *testing.T) {
	if g := goodbye(); !strings.Contains(g, "Goodbye") {
		t.Errorf("goodbye: %s", g)
	}
	if e := errorBanner(errors.New("kaput")); !strings.Contains(e, "kaput") {
		t.Errorf("error banner: %s", e)
	}
}

func TestRunStartStop(t *testing.T) {
	fb := newFakeBackend()
	out := &safeBuf{}
	addrCh := make(chan net.Addr, 1)
	d := deps{sp: fb,
		dial:  dialerWith(&net.UDPAddr{IP: net.ParseIP("10.1.2.3")}, nil),
		ready: func(a net.Addr) { addrCh <- a }}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- runWith(ctx, []string{"-ip", "127.0.0.1", "--port", "0"}, out, d) }()

	addr := <-addrCh
	resp, err := http.Get("http://" + addr.String() + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"volume":42`) {
		t.Fatalf("status: %d %s", resp.StatusCode, body)
	}
	<-fb.ran
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop")
	}
	select {
	case <-fb.done:
	default:
		t.Fatal("backend Run not finished")
	}
	s := out.String()
	for _, w := range []string{"REMOTE CONTROL AVAILABLE", "Goodbye"} {
		if !strings.Contains(s, w) {
			t.Errorf("output missing %q", w)
		}
	}
}

func TestRunBadFlagsAndHelp(t *testing.T) {
	d := deps{sp: newFakeBackend(), dial: dialerWith(nil, errors.New("x"))}
	if err := runWith(context.Background(), []string{"--port", "abc"}, io.Discard, d); err == nil {
		t.Error("expected flag error")
	}
	if err := runWith(context.Background(), []string{"-h"}, io.Discard, d); err != nil {
		t.Errorf("help: %v", err)
	}
}

func TestRunPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	d := deps{sp: newFakeBackend(), dial: dialerWith(nil, errors.New("x"))}
	err = runWith(context.Background(), []string{"-ip", "127.0.0.1", "-port", strconv.Itoa(port)}, io.Discard, d)
	if err == nil {
		t.Fatal("expected listen error")
	}
}

type stuckBackend struct{ *fakeBackend }

func (stuckBackend) Run(ctx context.Context) { <-ctx.Done(); select {} }

func TestShutdownWaitIsBounded(t *testing.T) {
	out := &safeBuf{}
	addrCh := make(chan net.Addr, 1)
	d := deps{sp: stuckBackend{newFakeBackend()},
		dial:      dialerWith(&net.UDPAddr{IP: net.ParseIP("10.1.2.3")}, nil),
		waitLimit: 50 * time.Millisecond,
		ready:     func(a net.Addr) { addrCh <- a }}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- runWith(ctx, []string{"-ip", "127.0.0.1", "--port", "0"}, out, d) }()
	<-addrCh
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runWith hung on stuck backend")
	}
	if !strings.Contains(out.String(), "exiting anyway") {
		t.Errorf("no log: %s", out.String())
	}
}

func TestFailExit(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		goos string
		tty  bool
		wait bool
	}{{"windows", true, true}, {"windows", false, false}, {"linux", true, false}}
	for _, c := range cases {
		out := &safeBuf{}
		in := strings.NewReader("\n")
		failExit(boom, out, in, c.goos, c.tty)
		if !strings.Contains(out.String(), "boom") {
			t.Errorf("%+v: banner missing", c)
		}
		if got := strings.Contains(out.String(), "Press ENTER"); got != c.wait {
			t.Errorf("%+v: prompt=%v", c, got)
		}
		if consumed := in.Len() == 0; consumed != c.wait {
			t.Errorf("%+v: stdin consumed=%v", c, consumed)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("regular file is not a terminal")
	}
	f.Close()
	if isTerminal(f) {
		t.Error("closed file is not a terminal")
	}
}

func TestPickAddrs(t *testing.T) {
	ip := net.ParseIP
	ifs := []ifaceAddrs{
		{"lo", true, []net.IP{ip("127.0.0.1")}},
		{"docker0", true, []net.IP{ip("172.17.0.1")}},
		{"vEthernet (WSL)", true, []net.IP{ip("172.28.0.1")}},
		{"vpn", true, []net.IP{ip("10.5.0.2")}},
		{"wifi", true, []net.IP{ip("192.168.1.101"), ip("fe80::1")}},
		{"down", false, []net.IP{ip("192.168.9.9")}},
		{"apipa", true, []net.IP{ip("169.254.1.1")}},
	}
	got := pickAddrs(ifs, "10.5.0.2")
	if len(got) != 2 || got[0] != (hostAddr{"wifi", "192.168.1.101"}) || got[1] != (hostAddr{"vpn", "10.5.0.2"}) {
		t.Fatalf("got %+v", got)
	}
	if got := pickAddrs(nil, "127.0.0.1"); len(got) != 1 || got[0].IP != "127.0.0.1" {
		t.Fatalf("fallback: %+v", got)
	}
	tie := pickAddrs([]ifaceAddrs{{"a", true, []net.IP{ip("192.168.1.2")}}, {"b", true, []net.IP{ip("192.168.2.2")}}}, "192.168.2.2")
	if tie[0].IP != "192.168.2.2" {
		t.Fatalf("primary should win ties: %+v", tie)
	}
}

func TestBannerMultipleAddrs(t *testing.T) {
	b := banner([]hostAddr{{"wifi", "192.168.1.101"}, {"vpn", "10.5.0.2"}}, 5000, "0.0.0.0")
	for _, w := range []string{"http://192.168.1.101:5000", "(wifi)", "http://10.5.0.2:5000", "VPN"} {
		if !strings.Contains(b, w) {
			t.Errorf("missing %q in %s", w, b)
		}
	}
}

func TestPortHint(t *testing.T) {
	e := errorBanner(errors.New("listen tcp 0.0.0.0:5000: bind: address already in use"))
	if !strings.Contains(e, "--port 5001") {
		t.Errorf("no hint: %s", e)
	}
	if strings.Contains(errorBanner(errors.New("other")), "--port 5001") {
		t.Error("hint shown for unrelated error")
	}
}

func TestWebURLs(t *testing.T) {
	addrs := []hostAddr{{"wifi", "192.168.1.101"}, {"vpn", "10.5.0.2"}}
	for _, c := range []struct {
		host string
		want []string
	}{
		{"0.0.0.0", []string{"http://192.168.1.101:5000", "http://10.5.0.2:5000"}},
		{"::", []string{"http://192.168.1.101:5000", "http://10.5.0.2:5000"}},
		{"", []string{"http://192.168.1.101:5000", "http://10.5.0.2:5000"}},
		{"192.168.1.7", []string{"http://192.168.1.7:5000"}},
		{"::1", []string{"http://[::1]:5000"}},
		{"desk.local", []string{"http://desk.local:5000"}},
	} {
		if got := webURLs(addrs, 5000, c.host); !slices.Equal(got, c.want) {
			t.Errorf("host %q: got %v, want %v", c.host, got, c.want)
		}
	}
}

func TestLogMode(t *testing.T) {
	for _, c := range []struct {
		args string
		want string
	}{
		{"", ""},
		{"--port 5001", ""},
		{"--log file", "file"},
		{"-log=terminal", "terminal"},
		{"--port 5001 --log=file --no-window", "file"},
		{"--log file --log terminal", "terminal"},
		{"--log nonsense", ""},
		{"--log", ""},
		{"--peers log --port 1", ""},
		{"-- --log file", ""},
	} {
		if got := logMode(strings.Fields(c.args)); got != c.want {
			t.Errorf("%q: got %q, want %q", c.args, got, c.want)
		}
	}
}

func TestLogFlagRejectsUnknownValue(t *testing.T) {
	err := runWith(context.Background(), []string{"--log", "nonsense"}, io.Discard, deps{})
	if err == nil || !strings.Contains(err.Error(), "--log") {
		t.Fatalf("got %v", err)
	}
}
