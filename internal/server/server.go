// Package server exposes the HTTP API and web UI for the Z407 remote.
package server

import (
	"embed"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/logx"
)

//go:embed index.html
var indexHTML []byte

// static holds the files that make the page installable on a phone: the web
// app manifest, its icons and the service worker.
//
//go:generate go run icongen.go
//go:embed static
var static embed.FS

const notConnectedMsg = "Speakers not connected. Waiting for background connection..."

// Speaker is the subset of speaker.Manager used by the HTTP handlers.
type Speaker interface {
	Status() (connected bool, volume int)
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

type srv struct {
	sp       Speaker
	log      *logx.Logger
	cmds     map[string]func() error
	presence *Presence
}

// New returns the HTTP handler serving the UI and the JSON API.
func New(sp Speaker, opts ...Option) http.Handler {
	s := &srv{sp: sp, log: logx.New(os.Stderr)}
	for _, o := range opts {
		o(s)
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
	writeJSON(w, http.StatusOK, map[string]any{"connected": connected, "volume": vol})
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

// accessLog logs every request except the UI's 5s GET /api/status poll and
// its event stream, e.g. "POST /api/vol_up -> 200 (2ms) from 192.168.1.50".
func (s *srv) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		if r.Method == http.MethodGet && (r.URL.Path == "/api/status" || r.URL.Path == "/api/events") {
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
