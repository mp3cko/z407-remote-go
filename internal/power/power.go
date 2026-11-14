// Package power tells the program when macOS puts the computer to sleep and
// wakes it, and keeps macOS from napping the program while it serves the
// remote. Elsewhere it does nothing.
package power

import "sync"

var (
	mu          sync.Mutex
	sleep, wake func()
)

func handlers() (s, w func()) {
	mu.Lock()
	defer mu.Unlock()
	return sleep, wake
}

func set(s, w func()) {
	mu.Lock()
	defer mu.Unlock()
	sleep, wake = s, w
}
