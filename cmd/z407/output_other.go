//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// unattended is true when the program was started from the macOS app, where
// nobody sees the output: a startup failure has to be shown in a dialog.
var unattended bool

// setupOutput applies --log: the terminal is the default, "file" sends the
// output to the log file instead. The macOS app started from Finder has no
// terminal, so there the file is the default.
func setupOutput(mode string) {
	if exe, err := os.Executable(); mode == "" && err == nil && isAppBundle(exe) && !isTerminal(os.Stdout) {
		mode, unattended = logFile, true
	}
	if mode != logFile {
		return
	}
	term := os.Stdout
	announceLogFile()
	if _, err := logToFile(); err != nil {
		fmt.Fprintf(term, "Cannot open the log file (%v): logging to the terminal\n", err)
	}
}

// alertFailure shows a startup failure when there is no terminal to read it in.
func alertFailure(err error) {
	if !unattended {
		return
	}
	text := fmt.Sprintf("%v\n%s\nLog: %s", err, portHint(err), logPath())
	_ = exec.Command("osascript",
		"-e", "on run argv",
		"-e", `display alert "Z407 Remote could not start" message (item 1 of argv) as critical`,
		"-e", "end run", "--", text).Run()
}
