//go:build linux

package media

import (
	"errors"
	"fmt"
	"os/exec"
)

var keysyms = map[Key]string{
	PlayPause: "XF86AudioPlay",
	Next:      "XF86AudioNext",
	Prev:      "XF86AudioPrev",
	VolUp:     "XF86AudioRaiseVolume",
	VolDown:   "XF86AudioLowerVolume",
	Mute:      "XF86AudioMute",
}

type linuxPresser struct {
	run func(name string, args ...string) error
}

// New returns a Presser that shells out to xdotool.
func New() Presser {
	return &linuxPresser{run: func(name string, args ...string) error {
		return exec.Command(name, args...).Run()
	}}
}

func (p *linuxPresser) Press(k Key) error {
	sym, ok := keysyms[k]
	if !ok {
		return unknownKey(k)
	}
	err := p.run("xdotool", "key", sym)
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("media: xdotool not found; install it (e.g. apt install xdotool): %w", err)
	}
	return fmt.Errorf("media: xdotool key %s: %w", sym, err)
}
