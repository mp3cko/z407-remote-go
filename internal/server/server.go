// Package server exposes the HTTP API and web UI for the Z407 remote.
package server

import (
	"embed"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/mp3cko/z407-remote-anywhere/internal/logx"
	"github.com/mp3cko/z407-remote-anywhere/internal/peers"
)

//go:embed index.html
var indexHTML []byte

// static holds the files that make the page installable on a phone: the web
// app manifest, its icons and the service worker.
//
//go:generate go run icongen.go
//go:embed static
var static embed.FS

// Icon returns the app icon, a 512-pixel PNG.
func Icon() []byte {
	data, _ := static.ReadFile("static/icon-512.png")
	return data
}

const notConnectedMsg = "Speakers not connected. Waiting for background connection..."

// notHoldingMsg answers a request for the speakers from a device that does
// not hold them: it is still looking for them itself.
const notHoldingMsg = "This device does not hold the speakers: it is looking for them itself"

// Speaker is the subset of speaker.Manager used by the HTTP handlers.
type Speaker interface {
	Status() (connected bool, volume int)
	// Progress tells more while not connected: the phase of the attempt
	// under way ("searching", "connecting", or "" between attempts), and
	// what went wrong with the last one.
	Progress() (phase, problem string)
	// Release lets go of the speakers until Reconnect, so that another
	// device can connect. Progress then reports the phase "released".
	Release()
	Reconnect()
	VolumeUp() error
	VolumeDown() error
	PlayPause() error
	InputBluetooth() error
	InputAux() error
	InputUSB() error
	BluetoothPair() error
	FactoryReset() error
	BassUp() error
	BassDown() error
	NextTrack() error
	PrevTrack() error
}

// Option customises the handler.
type Option func(*srv)

// WithLogger sets the logger (default: colored output on stderr).
func WithLogger(l *logx.Logger) Option { return func(s *srv) { s.log = l } }

// WithPresence reports the pages open on this machine to p.
func WithPresence(p *Presence) Option { return func(s *srv) { s.presence = p } }

// WithPeers sets the store behind the "Other devices" editor (default: a
// list that lives in memory only).
func WithPeers(p *peers.Store) Option { return func(s *srv) { s.peers = p } }

// WithAgain sets what POST /api/takeover does: ask the other devices for the
// speakers once more (default: nothing).
func WithAgain(f func()) Option { return func(s *srv) { s.again = f } }

// WithAddrs sets the function listing the addresses the remote can be
// opened at from other devices ("http://192.168.1.20:2407"), best guess
// first. The page shows them (default: none).
func WithAddrs(fn func() []string) Option { return func(s *srv) { s.addrs = fn } }

// WithNotice sets the passing message the display shows (default: none).
func WithNotice(n *Notice) Option { return func(s *srv) { s.notice = n } }

// WithHistory enables the log viewer: GET /api/log serves the lines in h.
func WithHistory(h *logx.History) Option { return func(s *srv) { s.history = h } }

type srv struct {
	sp       Speaker
	log      *logx.Logger
	cmds     map[string]func() error
	presence *Presence
	peers    *peers.Store
	addrs    func() []string
	notice   *Notice
	history  *logx.History
	again    func()
}

// New returns the HTTP handler serving the UI and the JSON API.
func New(sp Speaker, opts ...Option) http.Handler {
	s := &srv{sp: sp, log: logx.New(os.Stderr)}
	for _, o := range opts {
		o(s)
	}
	if s.peers == nil {
		s.peers, _ = peers.Open("", peers.DefaultPort)
	}
	s.cmds = map[string]func() error{
		"vol_up":          sp.VolumeUp,
		"vol_down":        sp.VolumeDown,
		"play_pause":      sp.PlayPause,
		"input_aux":       sp.InputAux,
		"input_bluetooth": sp.InputBluetooth,
		"bluetooth_pair":  sp.BluetoothPair,
		"factory_reset":   sp.FactoryReset,
		"input_usb":       sp.InputUSB,
		"bass_up":         sp.BassUp,
		"bass_down":       sp.BassDown,
		"next_speaker":    sp.NextTrack,
		"prev_speaker":    sp.PrevTrack,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /{file}", s.staticFile)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/addrs", s.listAddrs)
	mux.HandleFunc("GET /api/log", s.logLines)
	mux.HandleFunc("GET /api/peers", s.listPeers)
	mux.HandleFunc("POST /api/peers", s.savePeers)
	mux.HandleFunc("POST /api/disconnect", s.disconnect)
	mux.HandleFunc("POST /api/reconnect", s.reconnect)
	mux.HandleFunc("POST /api/takeover", s.takeover)
	mux.HandleFunc("POST /api/{command}", s.command)
	return s.accessLog(mux)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *srv) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

// staticFile serves the embedded files next to the page.
func (s *srv) staticFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	data, err := static.ReadFile("static/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctype := mime.TypeByExtension(path.Ext(name))
	switch path.Ext(name) {
	case ".webmanifest":
		ctype = "application/manifest+json"
	case ".js":
		ctype = "text/javascript; charset=utf-8"
		w.Header().Set("Cache-Control", "no-cache") // browsers must notice a new service worker
	}
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write(data)
}

func (s *srv) status(w http.ResponseWriter, _ *http.Request) {
	connected, vol := s.sp.Status()
	phase, problem := s.sp.Progress()
	switch {
	case connected:
		phase, problem = "connected", ""
	case phase == "":
		phase = "waiting" // for the next attempt
	}
	note := ""
	if s.notice != nil {
		note = s.notice.Get()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connected": connected, "note": note, "phase": phase, "problem": problem, "volume": vol,
	})
}

func (s *srv) command(w http.ResponseWriter, r *http.Request) {
	if connected, _ := s.sp.Status(); !connected {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": notConnectedMsg})
		return
	}
	fn, ok := s.cmds[r.PathValue("command")]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Unknown command"})
		return
	}
	if err := fn(); err != nil {
		s.log.Errorf("command execution error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// logLines serves the log viewer: the remembered lines after line number
// ?after= (all of them without it), and the number to ask with next time.
func (s *srv) logLines(w http.ResponseWriter, r *http.Request) {
	lines := []map[string]any{}
	last := 0
	if s.history != nil {
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		var entries []logx.Entry
		entries, last = s.history.Since(after)
		for _, e := range entries {
			lines = append(lines, map[string]any{
				"level": e.Level.String(), "n": e.N, "text": e.Text, "time": e.Time.Format("15:04:05"),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "next": last})
}

func (s *srv) listAddrs(w http.ResponseWriter, _ *http.Request) {
	urls := []string{}
	if s.addrs != nil {
		urls = append(urls, s.addrs()...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"urls": urls})
}

func (s *srv) listPeers(w http.ResponseWriter, _ *http.Request) {
	list, fixed := s.peers.List()
	writeJSON(w, http.StatusOK, map[string]any{"peers": list, "fixed": fixed})
}

// savePeers stores the comma-separated ?list= parameter. The list travels in
// the query string because the Android app, which shows this same page,
// cannot read the body of a request it intercepts.
func (s *srv) savePeers(w http.ResponseWriter, r *http.Request) {
	list, err := s.peers.Save(r.URL.Query().Get("list"))
	switch {
	case errors.Is(err, peers.ErrFixed):
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "peers": list})
	}
}

// disconnect lets another device take the speakers over (they accept one
// controller at a time): the app lets go of them and stays open. Its page
// says so and offers to reconnect.
//
// An app that has not even seen the speakers holds nothing to let go of. It
// refuses and goes on looking: two copies that start at the same moment ask
// each other at once, and if both let go, neither connects.
func (s *srv) disconnect(w http.ResponseWriter, _ *http.Request) {
	connected, _ := s.sp.Status()
	if phase, _ := s.sp.Progress(); !connected && phase != "connecting" && phase != "released" {
		s.log.Infof("Another device asked for the speakers, but this one does not hold them: looking for them on")
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "error": notHoldingMsg})
		return
	}
	// Closing the link can take a moment; the caller does not wait for it.
	go s.sp.Release()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// reconnect ends what disconnect began.
func (s *srv) reconnect(w http.ResponseWriter, _ *http.Request) {
	s.sp.Reconnect()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// takeover asks the other devices for the speakers once more, also those
// that were asked before: the page offers it while the speakers cannot be
// found. The answer comes before theirs.
func (s *srv) takeover(w http.ResponseWriter, _ *http.Request) {
	if s.again != nil {
		s.again()
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) {
	w.code = c
	w.ResponseWriter.WriteHeader(c)
}

// Unwrap lets http.ResponseController reach the real writer (for Flush).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// accessLog logs every request except the UI's polls (GET /api/status and,
// while the log viewer is open, GET /api/log) and its event stream, e.g. "POST /api/vol_up -> 200 (2ms) from 192.168.1.50".
func (s *srv) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		if r.Method == http.MethodGet && (r.URL.Path == "/api/status" || r.URL.Path == "/api/log" || r.URL.Path == "/api/events") {
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		level := logx.Info
		if sw.code >= http.StatusBadRequest {
			level = logx.Error // failed requests stand out
		}
		s.log.Logf(level, "%s %s -> %d (%s) from %s", r.Method, r.URL.Path, sw.code, time.Since(start).Round(time.Microsecond), host)
	})
}
