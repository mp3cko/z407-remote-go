package server

import (
	"sync"
	"time"
)

// NoticeTime is how long a Notice stays on the display.
const NoticeTime = 8 * time.Second

// Notice is a short message for the remote's display, such as "Asking
// living-room-pc:5000 to release the speakers". It goes away by itself.
// The zero value is ready to use.
type Notice struct {
	mu    sync.Mutex
	text  string
	until time.Time
	now   func() time.Time // nil = time.Now
}

func (n *Notice) clock() time.Time {
	if n.now != nil {
		return n.now()
	}
	return time.Now()
}

// Set shows text for NoticeTime, replacing what was shown.
func (n *Notice) Set(text string) {
	n.mu.Lock()
	n.text, n.until = text, n.clock().Add(NoticeTime)
	n.mu.Unlock()
}

// Get returns the text to show now, "" when there is none.
func (n *Notice) Get() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.text == "" || !n.clock().Before(n.until) {
		return ""
	}
	return n.text
}
