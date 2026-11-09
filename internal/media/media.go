// Package media sends OS-level media key presses.
package media

import "fmt"

// Key is a media key.
type Key string

const (
	PlayPause Key = "playpause"
	Next      Key = "nexttrack"
	Prev      Key = "prevtrack"
	VolUp     Key = "volumeup"
	VolDown   Key = "volumedown"
	Mute      Key = "volumemute"
)

// Presser presses a media key on the host OS.
type Presser interface {
	Press(Key) error
}

func unknownKey(k Key) error {
	return fmt.Errorf("media: unknown key %q", string(k))
}
