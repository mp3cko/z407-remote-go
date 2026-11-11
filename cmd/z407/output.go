package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mp3cko/z407-remote-go/internal/logx"
)

// Values of the --log flag.
const (
	logTerminal = "terminal"
	logFile     = "file"
)

// logMode finds the value of --log in args ahead of the real flag parsing,
// because the output has to be in place before anything is printed. It
// returns "" when the flag is absent or its value unknown; runWith is the
// one to report a mistake.
func logMode(args []string) string {
	mode := ""
	for i := 0; i < len(args) && args[i] != "--"; i++ {
		if !strings.HasPrefix(args[i], "-") {
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(args[i], "-"), "-"), "=")
		if name != "log" {
			continue
		}
		if !hasVal && i+1 < len(args) {
			i++
			val = args[i]
		}
		mode = val // the last one wins, as in package flag
	}
	if mode != logTerminal && mode != logFile {
		return ""
	}
	return mode
}

// logPath is where the log file lives: the user's cache directory
// (%LOCALAPPDATA% on Windows, ~/.cache on Linux, ~/Library/Caches on macOS).
func logPath() string {
	dir, _ := os.UserCacheDir()
	return logFilePath(dir, os.TempDir())
}

// logToFile sends standard output and standard error to the log file.
func logToFile() (*os.File, error) {
	f, err := openLogFile(logPath())
	if err != nil {
		return nil, err
	}
	os.Stdout, os.Stderr = f, f
	logx.UseStdout(f)
	return f, nil
}

// announceLogFile tells the terminal, if there is one, where the output goes.
func announceLogFile() {
	fmt.Fprintf(os.Stdout, "Logging to %s\n", logPath())
}
