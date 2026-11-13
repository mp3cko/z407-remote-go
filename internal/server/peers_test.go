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
	if m := decode(t, rec); rec.Code != 200 || !reflect.DeepEqual(peerList(t, m), []string{"pc:2407"}) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

// Another device asks for the speakers: the app lets go of them and stays
// open, its page says so, and "reconnect" takes them back.
func TestDisconnectAndReconnect(t *testing.T) {
	sp := &fakeSpeaker{connected: true, volume: 40}
	h := New(sp, quiet)
	rec := do(h, "POST", "/api/disconnect")
	if m := decode(t, rec); rec.Code != 200 || len(m) != 1 || m["success"] != true {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !reflect.DeepEqual(sp.got(), []string{"Release"}) {
		if time.Now().After(deadline) {
			t.Fatalf("speakers not released: %v", sp.got())
		}
		time.Sleep(time.Millisecond)
	}
	if got := do(h, "GET", "/api/status").Body.String(); got != `{"connected":false,"note":"","phase":"released","problem":"","volume":40}`+"\n" {
		t.Errorf("status while released: %s", got)
	}
	if rec := do(h, "POST", "/api/vol_up"); rec.Code != 404 {
		t.Errorf("command while released: %d", rec.Code)
	}

	rec = do(h, "POST", "/api/reconnect")
	if m := decode(t, rec); rec.Code != 200 || len(m) != 1 || m["success"] != true {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if !reflect.DeepEqual(sp.got(), []string{"Release", "Reconnect"}) {
		t.Errorf("calls: %v", sp.got())
	}
	if got := decode(t, do(h, "GET", "/api/status"))["phase"]; got != "waiting" {
		t.Errorf("phase after reconnect: %v", got)
	}
	for _, path := range []string{"/api/disconnect", "/api/reconnect"} {
		if rec := do(h, "GET", path); rec.Code != 405 {
			t.Errorf("GET %s: code=%d", path, rec.Code)
		}
	}
}

// The caller must not wait for the link to close.
func TestDisconnectAnswersFirst(t *testing.T) {
	sp := &fakeSpeaker{}
	sp.mu.Lock() // Release blocks
	defer sp.mu.Unlock()
	ts := httptest.NewServer(New(sp, quiet))
	defer ts.Close()
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Post(ts.URL+"/api/disconnect", "", nil)
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
	for _, w := range []string{`id="devOpen"`, `<dialog id="devices"`, "fetch('/api/peers')", "'/api/peers?list=' + encodeURIComponent("} {
		if !strings.Contains(page, w) {
			t.Errorf("page lacks %q", w)
		}
	}
	// The key that opens the editor is on the remote itself, not below it.
	if i, j := strings.Index(page, `id="devOpen"`), strings.Index(page, `class="panel"`); i < 0 || j < i {
		t.Error("the Devices key should be on the remote")
	}
}
