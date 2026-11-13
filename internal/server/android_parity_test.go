package server

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/mp3cko/z407-remote-anywhere/internal/logx"
)

// The Android app shows this same page and answers its requests natively
// (android/.../core/Api.kt). Its tests (ApiTest.kt) assert these very
// bodies, so a change here must be made there too.
func TestBodiesSharedWithAndroid(t *testing.T) {
	q := url.QueryEscape
	connected := func() *fakeSpeaker { return &fakeSpeaker{connected: true, volume: 55} }
	failing := connected()
	failing.err = errors.New("write 8002: boom")
	withPeers := New(&fakeSpeaker{}, quiet)
	do(withPeers, "POST", "/api/peers?list="+q("desk, 192.168.1.20:5001"))

	for _, c := range []struct {
		name   string
		h      func() *fakeSpeaker
		method string
		path   string
		code   int
		body   string
	}{
		{"status", func() *fakeSpeaker { return &fakeSpeaker{volume: 50} }, "GET", "/api/status", 200,
			`{"connected":false,"note":"","phase":"waiting","problem":"","volume":50}`},
		{"status searching", func() *fakeSpeaker {
			return &fakeSpeaker{volume: 50, phase: "searching", problem: "Speakers not found"}
		},
			"GET", "/api/status", 200, `{"connected":false,"note":"","phase":"searching","problem":"Speakers not found","volume":50}`},
		{"status connected", connected, "GET", "/api/status", 200,
			`{"connected":true,"note":"","phase":"connected","problem":"","volume":55}`},
		{"no log", connected, "GET", "/api/log", 200, `{"lines":[],"next":0}`},
		{"command", connected, "POST", "/api/vol_up", 200, `{"success":true}`},
		{"not connected", func() *fakeSpeaker { return &fakeSpeaker{} }, "POST", "/api/vol_up", 404,
			`{"error":"Speakers not connected. Waiting for background connection...","success":false}`},
		{"unknown command", connected, "POST", "/api/bogus", 400, `{"error":"Unknown command","success":false}`},
		{"speaker error", func() *fakeSpeaker { return failing }, "POST", "/api/vol_up", 500,
			`{"error":"write 8002: boom","success":false}`},
		{"no addresses", connected, "GET", "/api/addrs", 200, `{"urls":[]}`},
		{"no peers", connected, "GET", "/api/peers", 200, `{"fixed":false,"peers":[]}`},
		{"save peers", connected, "POST", "/api/peers?list=" + q("desk, 192.168.1.20:5001"), 200,
			`{"peers":["desk:2407","192.168.1.20:5001"],"success":true}`},
		{"empty peers", connected, "POST", "/api/peers?list=", 200, `{"peers":[],"success":true}`},
		{"bad peer", connected, "POST", "/api/peers?list=" + q("good-pc, http://bad/"), 400,
			`{"error":"\"http://bad/\" is not a valid address (leave out http:// and any path). Use a name or IP address, optionally with a port: living-room-pc or 192.168.1.20:2408","success":false}`},
		{"escaping", connected, "POST", "/api/peers?list=" + q("<a&b>"), 400,
			`{"error":"\"\u003ca\u0026b\u003e\" is not a valid address (the name may only contain letters, digits, hyphens and dots). Use a name or IP address, optionally with a port: living-room-pc or 192.168.1.20:2408","success":false}`},
		{"disconnect", connected, "POST", "/api/disconnect", 200, `{"success":true}`},
		{"reconnect", connected, "POST", "/api/reconnect", 200, `{"success":true}`},
		{"status released", func() *fakeSpeaker { return &fakeSpeaker{volume: 50, phase: "released"} },
			"GET", "/api/status", 200, `{"connected":false,"note":"","phase":"released","problem":"","volume":50}`},
	} {
		rec := do(New(c.h(), quiet), c.method, c.path)
		if rec.Code != c.code || rec.Body.String() != c.body+"\n" {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	// The log viewer: every line, then only the ones after a given number.
	history := logx.NewHistory(10)
	history.SetClock(func() time.Time { return time.Date(2026, 1, 2, 21, 4, 5, 0, time.Local) })
	history.Add(logx.Info, "Searching")
	history.Add(logx.Error, `Dial failed: "x" <y>`)
	history.Add(logx.Success, "Connected to Z407")
	notice := &Notice{}
	notice.Set("Asking desk:5000 to release the speakers")
	withLog := New(&fakeSpeaker{volume: 50}, quiet, WithHistory(history), WithNotice(notice))
	for path, want := range map[string]string{
		"/api/log": `{"lines":[{"level":"info","n":1,"text":"Searching","time":"21:04:05"},` +
			`{"level":"error","n":2,"text":"Dial failed: \"x\" \u003cy\u003e","time":"21:04:05"},` +
			`{"level":"ok","n":3,"text":"Connected to Z407","time":"21:04:05"}],"next":3}`,
		"/api/log?after=2": `{"lines":[{"level":"ok","n":3,"text":"Connected to Z407","time":"21:04:05"}],"next":3}`,
		"/api/log?after=3": `{"lines":[],"next":3}`,
		"/api/log?after=junk": `{"lines":[{"level":"info","n":1,"text":"Searching","time":"21:04:05"},` +
			`{"level":"error","n":2,"text":"Dial failed: \"x\" \u003cy\u003e","time":"21:04:05"},` +
			`{"level":"ok","n":3,"text":"Connected to Z407","time":"21:04:05"}],"next":3}`,
		"/api/status": `{"connected":false,"note":"Asking desk:5000 to release the speakers","phase":"waiting","problem":"","volume":50}`,
	} {
		if rec := do(withLog, "GET", path); rec.Code != 200 || rec.Body.String() != want+"\n" {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}

	if rec := do(withPeers, "GET", "/api/peers"); rec.Body.String() != `{"fixed":false,"peers":["desk:2407","192.168.1.20:5001"]}`+"\n" {
		t.Errorf("saved peers: %s", rec.Body)
	}
}
