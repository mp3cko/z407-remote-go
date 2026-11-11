package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/peers"
)

func peerList(t *testing.T, m map[string]any) []string {
	t.Helper()
	raw, ok := m["peers"].([]any)
	if !ok {
		t.Fatalf("peers is %#v, want a JSON array", m["peers"])
	}
	out := []string{}
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func TestPeersReadAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, _ := peers.Open(path, 5000)
	h := New(&fakeSpeaker{}, quiet, WithPeers(store))

	rec := do(h, "GET", "/api/peers")
	m := decode(t, rec)
	if rec.Code != 200 || len(m) != 2 || len(peerList(t, m)) != 0 || m["fixed"] != false {
		t.Fatalf("empty list: code=%d body=%s", rec.Code, rec.Body)
	}

	// Saved without a request body: the list is in the query string.
	rec = do(h, "POST", "/api/peers?list="+url.QueryEscape("desk, 192.168.1.20:5001"))
	m = decode(t, rec)
	want := []string{"desk:5000", "192.168.1.20:5001"}
	if rec.Code != 200 || m["success"] != true || !reflect.DeepEqual(peerList(t, m), want) {
		t.Fatalf("save: code=%d body=%s", rec.Code, rec.Body)
	}
	if got := peerList(t, decode(t, do(h, "GET", "/api/peers"))); !reflect.DeepEqual(got, want) {
		t.Fatalf("read back: %v", got)
	}

	// A restart (new store, new handler) still has it.
	store2, err := peers.Open(path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	h2 := New(&fakeSpeaker{}, quiet, WithPeers(store2))
	if got := peerList(t, decode(t, do(h2, "GET", "/api/peers"))); !reflect.DeepEqual(got, want) {
		t.Fatalf("after restart: %v", got)
	}

	// The list may be emptied, with or without the parameter.
	for _, q := range []string{"/api/peers?list=", "/api/peers"} {
		rec = do(h, "POST", q)
		if m := decode(t, rec); rec.Code != 200 || len(peerList(t, m)) != 0 {
			t.Fatalf("%s: code=%d body=%s", q, rec.Code, rec.Body)
		}
	}
}

func TestPeersValidation(t *testing.T) {
	store, _ := peers.Open(filepath.Join(t.TempDir(), "config.json"), 5000)
	h := New(&fakeSpeaker{}, quiet, WithPeers(store))
	do(h, "POST", "/api/peers?list=good-pc")

	rec := do(h, "POST", "/api/peers?list="+url.QueryEscape("good-pc, http://bad/"))
	m := decode(t, rec)
	msg, _ := m["error"].(string)
	if rec.Code != 400 || m["success"] != false || !strings.Contains(msg, `"http://bad/" is not a valid address`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if got := peerList(t, decode(t, do(h, "GET", "/api/peers"))); !reflect.DeepEqual(got, []string{"good-pc:5000"}) {
		t.Fatalf("a rejected save changed the list: %v", got)
	}
}

func TestPeersFixedByFlag(t *testing.T) {
	store, _ := peers.Open(filepath.Join(t.TempDir(), "config.json"), 5000)
	if err := store.Fix("flag-pc"); err != nil {
		t.Fatal(err)
	}
	h := New(&fakeSpeaker{}, quiet, WithPeers(store))
	m := decode(t, do(h, "GET", "/api/peers"))
	if m["fixed"] != true || !reflect.DeepEqual(peerList(t, m), []string{"flag-pc:5000"}) {
		t.Fatalf("list: %v", m)
	}
	rec := do(h, "POST", "/api/peers?list=other")
	m = decode(t, rec)
	if rec.Code != 409 || m["success"] != false || !strings.Contains(m["error"].(string), "--peers") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestPeersDefaultStore(t *testing.T) {
	h := New(&fakeSpeaker{}, quiet)
	rec := do(h, "POST", "/api/peers?list=pc")
	if m := decode(t, rec); rec.Code != 200 || !reflect.DeepEqual(peerList(t, m), []string{"pc:5000"}) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestShutdownEndpoint(t *testing.T) {
	called := make(chan struct{})
	// Works whether or not the speakers are connected.
	h := New(&fakeSpeaker{}, quiet, WithShutdown(func() { close(called) }))
	rec := do(h, "POST", "/api/shutdown")
	m := decode(t, rec)
	if rec.Code != 200 || len(m) != 1 || m["success"] != true {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown function not called")
	}
	if rec := do(h, "GET", "/api/shutdown"); rec.Code != 405 {
		t.Errorf("GET: code=%d", rec.Code)
	}
}

func TestShutdownUnavailable(t *testing.T) {
	sp := &fakeSpeaker{connected: true}
	rec := do(New(sp, quiet), "POST", "/api/shutdown")
	if m := decode(t, rec); rec.Code != 501 || m["success"] != false {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if len(sp.got()) != 0 {
		t.Error("shutdown reached the speaker")
	}
}

// The answer must be on the wire before the app goes down.
func TestShutdownAnswersFirst(t *testing.T) {
	block := make(chan struct{})
	ts := httptest.NewServer(New(&fakeSpeaker{}, quiet, WithShutdown(func() { <-block })))
	defer ts.Close()
	defer close(block)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Post(ts.URL+"/api/shutdown", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestPeersEditorInPage(t *testing.T) {
	page := do(New(&fakeSpeaker{}, quiet), "GET", "/").Body.String()
	for _, w := range []string{"<summary>Other computers</summary>", "fetch('/api/peers')", "'/api/peers?list=' + encodeURIComponent("} {
		if !strings.Contains(page, w) {
			t.Errorf("page lacks %q", w)
		}
	}
	if i, j := strings.Index(page, "<summary>Troubleshooting</summary>"), strings.Index(page, "<summary>Other computers</summary>"); i < 0 || j < i {
		t.Error("the editor should follow Troubleshooting")
	}
}
