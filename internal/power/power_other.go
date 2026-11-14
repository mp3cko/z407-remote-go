//go:build !darwin || !cgo

package power

// Watch does nothing here: sleep and wake are not reported.
func Watch(sleepFn, wakeFn func()) error {
	set(sleepFn, wakeFn)
	return nil
}

// NoNap does nothing here.
func NoNap() {}
