//go:build !windows

package main

import (
	"fmt"
	"os"
)

// setupOutput applies --log: the terminal is the default, "file" sends the
// output to the log file instead.
func setupOutput(mode string) {
	if mode != logFile {
		return
	}
	term := os.Stdout
	announceLogFile()
	if _, err := logToFile(); err != nil {
		fmt.Fprintf(term, "Cannot open the log file (%v): logging to the terminal\n", err)
	}
}

// alertFailure is only needed on Windows; see output_windows.go.
func alertFailure(error) {}
