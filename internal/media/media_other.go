//go:build !windows && !linux && !darwin

package media

import (
	"fmt"
	"runtime"
)

type unsupported struct{}

// New returns a Presser that always fails on this OS.
func New() Presser { return unsupported{} }

func (unsupported) Press(Key) error {
	return fmt.Errorf("media: media keys not supported on %s", runtime.GOOS)
}
