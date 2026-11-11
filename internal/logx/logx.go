// Package logx provides leveled console logging and ANSI colors that switch
// themselves off when the output is not a terminal or NO_COLOR is set.
package logx

import (
	"fmt"
	"io"
	"log"
	"os"
)

// Level classifies a log line and picks its color.
type Level int

const (
	Info    Level = iota // blue
	Success              // green
	Error                // red
)

const (
	reset  = "\033[0m"
	red    = "\033[91m"
	green  = "\033[92m"
	yellow = "\033[93m"
	blue   = "\033[94m"
)

func (l Level) color() string {
	switch l {
	case Success:
		return green
	case Error:
		return red
	default:
		return blue
	}
}

// Enabled reports whether ANSI colors should be written to f: it must be a
// terminal that understands them, and NO_COLOR must not be set.
func Enabled(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return enableVT(f)
}

var stdoutColor = Enabled(os.Stdout)

// UseStdout re-evaluates the banner colors for f, for programs that replace
// standard output after start.
func UseStdout(f *os.File) { stdoutColor = Enabled(f) }

func paint(on bool, c, s string) string {
	if !on {
		return s
	}
	return c + s + reset
}

// Green, Yellow and Red color s for standard output (banners and the like).
func Green(s string) string  { return paint(stdoutColor, green, s) }
func Yellow(s string) string { return paint(stdoutColor, yellow, s) }
func Red(s string) string    { return paint(stdoutColor, red, s) }

// Logger writes timestamped lines, coloring the message by level.
type Logger struct {
	l     *log.Logger
	color bool
}

// New returns a Logger writing to f, with colors if f supports them.
func New(f *os.File) *Logger {
	return &Logger{l: log.New(f, "", log.LstdFlags), color: Enabled(f)}
}

// NewWriter returns a Logger for an arbitrary writer; color forces ANSI on or off.
func NewWriter(w io.Writer, color bool) *Logger {
	return &Logger{l: log.New(w, "", log.LstdFlags), color: color}
}

// Logf logs one line at the given level.
func (lg *Logger) Logf(level Level, format string, args ...any) {
	lg.l.Print(paint(lg.color, level.color(), fmt.Sprintf(format, args...)))
}

func (lg *Logger) Infof(format string, args ...any)    { lg.Logf(Info, format, args...) }
func (lg *Logger) Successf(format string, args ...any) { lg.Logf(Success, format, args...) }
func (lg *Logger) Errorf(format string, args ...any)   { lg.Logf(Error, format, args...) }
