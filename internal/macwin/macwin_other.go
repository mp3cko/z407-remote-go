//go:build !darwin || !cgo

package macwin

// Main runs run.
func Main(run func()) { run() }

// Available reports whether Open can open a window: never here.
func Available() bool { return false }

// Open fails with ErrUnavailable.
func Open(url, title string, width, height int, icon []byte) (*Window, error) {
	return nil, ErrUnavailable
}

func closeWindow() {}
