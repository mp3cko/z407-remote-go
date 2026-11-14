//go:build darwin && cgo

package power

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework IOKit

int power_watch(void);
void power_no_nap(void);
*/
import "C"

import (
	"errors"
	"sync"
)

var once sync.Once

// Watch calls sleep before the computer sleeps, and wake once it has woken
// up. The computer waits for sleep to return, so it must be quick. A later
// call replaces the functions.
func Watch(sleepFn, wakeFn func()) error {
	set(sleepFn, wakeFn)
	var err error
	once.Do(func() {
		if C.power_watch() != 0 {
			err = errors.New("power: cannot register for sleep notifications")
		}
	})
	return err
}

// NoNap keeps macOS from napping the program when its window is hidden or
// covered: other devices still ask it for the speakers, and the speakers
// expect their keep-alives answered. The computer may still sleep.
func NoNap() { C.power_no_nap() }

const (
	eventSleep = 1
	eventWake  = 2
)

//export goPowerEvent
func goPowerEvent(what C.int) {
	s, w := handlers()
	switch {
	case what == eventSleep && s != nil:
		s()
	case what == eventWake && w != nil:
		w()
	}
}
