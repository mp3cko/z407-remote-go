// Package server exposes the HTTP API and web UI for the Z407 remote.
package server

import (
	_ "embed"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"
)

//go:embed index.html
var indexHTML []byte

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

// WithLogger sets the logger (default: the standard logger).
func WithLogger(l *log.Logger) Option { return func(s *srv) { s.log = l } }

type srv struct {
	sp   Speaker
	log  *log.Logger
	cmds map[string]func() error
}

// New returns the HTTP handler serving the UI and the JSON API.
func New(sp Speaker, opts ...Option) http.Handler {
	s := &srv{sp: sp, log: log.Default()}
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
		s.log.Printf("command execution error: %v", err)
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

// accessLog logs every request except the UI's 5s GET /api/status poll,
// e.g. "POST /api/vol_up -> 200 (2ms) from 192.168.1.50".
func (s *srv) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		if r.Method == http.MethodGet && r.URL.Path == "/api/status" {
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		s.log.Printf("%s %s -> %d (%s) from %s", r.Method, r.URL.Path, sw.code, time.Since(start).Round(time.Microsecond), host)
	})
}
