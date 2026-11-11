//go:build !windows

package main

// setupOutput is only needed on Windows; see output_windows.go.
func setupOutput() {}

// alertFailure is only needed on Windows; see output_windows.go.
func alertFailure(error) {}
