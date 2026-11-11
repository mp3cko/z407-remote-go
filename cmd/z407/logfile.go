package main

import (
	"os"
	"path/filepath"
)

// maxLogSize is the size at which the log file starts over.
const maxLogSize = 1 << 20

// logFilePath is where the Windows build without a console writes its log:
// %LOCALAPPDATA%\z407\z407.log, or the temporary directory as a last resort.
func logFilePath(localAppData, tempDir string) string {
	base := localAppData
	if base == "" {
		base = tempDir
	}
	return filepath.Join(base, "z407", "z407.log")
}

// openLogFile opens path for appending, creating its directory. A file that
// has grown past maxLogSize is started afresh, so the log cannot fill the disk.
func openLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogSize {
		flags |= os.O_TRUNC
	}
	return os.OpenFile(path, flags, 0o644)
}
