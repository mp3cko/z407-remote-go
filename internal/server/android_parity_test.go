package server

import (
	"errors"
	"net/url"
	"testing"
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
			`{"connected":false,"volume":50}`},
		{"status connected", connected, "GET", "/api/status", 200, `{"connected":true,"volume":55}`},
		{"command", connected, "POST", "/api/vol_up", 200, `{"success":true}`},
		{"not connected", func() *fakeSpeaker { return &fakeSpeaker{} }, "POST", "/api/vol_up", 404,
			`{"error":"Speakers not connected. Waiting for background connection...","success":false}`},
		{"unknown command", connected, "POST", "/api/bogus", 400, `{"error":"Unknown command","success":false}`},
		{"speaker error", func() *fakeSpeaker { return failing }, "POST", "/api/vol_up", 500,
			`{"error":"write 8002: boom","success":false}`},
		{"no peers", connected, "GET", "/api/peers", 200, `{"fixed":false,"peers":[]}`},
		{"save peers", connected, "POST", "/api/peers?list=" + q("desk, 192.168.1.20:5001"), 200,
			`{"peers":["desk:5000","192.168.1.20:5001"],"success":true}`},
		{"empty peers", connected, "POST", "/api/peers?list=", 200, `{"peers":[],"success":true}`},
		{"bad peer", connected, "POST", "/api/peers?list=" + q("good-pc, http://bad/"), 400,
			`{"error":"\"http://bad/\" is not a valid address (leave out http:// and any path). Use a name or IP address, optionally with a port: living-room-pc or 192.168.1.20:5000","success":false}`},
		{"escaping", connected, "POST", "/api/peers?list=" + q("<a&b>"), 400,
			`{"error":"\"\u003ca\u0026b\u003e\" is not a valid address (the name may only contain letters, digits, hyphens and dots). Use a name or IP address, optionally with a port: living-room-pc or 192.168.1.20:5000","success":false}`},
		{"shutdown unavailable", connected, "POST", "/api/shutdown", 501,
			`{"error":"Shutdown is not available","success":false}`},
	} {
		rec := do(New(c.h(), quiet), c.method, c.path)
		if rec.Code != c.code || rec.Body.String() != c.body+"\n" {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	if rec := do(withPeers, "GET", "/api/peers"); rec.Body.String() != `{"fixed":false,"peers":["desk:5000","192.168.1.20:5001"]}`+"\n" {
		t.Errorf("saved peers: %s", rec.Body)
	}
}
