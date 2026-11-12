package logx

import (
	"sync"
	"time"
)

// String names the level for the log viewer.
func (l Level) String() string {
	switch l {
	case Success:
		return "ok"
	case Error:
		return "error"
	case Warning:
		return "warn"
	default:
		return "info"
	}
}

// Entry is one remembered log line. N counts the lines from 1.
type Entry struct {
	N     int
	Time  time.Time
	Level Level
	Text  string
}

// History remembers the last lines logged, for the log viewer of the remote.
// It is safe for concurrent use.
type History struct {
	mu      sync.Mutex
	max     int
	last    int // N of the newest entry
	entries []Entry
	now     func() time.Time
}

// NewHistory returns a History holding at most max lines.
func NewHistory(max int) *History {
	return &History{max: max, now: time.Now}
}

// SetClock replaces the clock that stamps the lines, for tests.
func (h *History) SetClock(now func() time.Time) { h.now = now }

// Add remembers a line.
func (h *History) Add(level Level, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last++
	h.entries = append(h.entries, Entry{N: h.last, Time: h.now(), Level: level, Text: text})
	if len(h.entries) > h.max {
		h.entries = append(h.entries[:0], h.entries[len(h.entries)-h.max:]...)
	}
}

// Since returns the remembered lines after line number after (0 = all of
// them) and the number of the newest line, which is the next "after".
func (h *History) Since(after int) (entries []Entry, last int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, e := range h.entries {
		if e.N > after {
			entries = append(entries, h.entries[i:]...)
			break
		}
	}
	return entries, h.last
}
