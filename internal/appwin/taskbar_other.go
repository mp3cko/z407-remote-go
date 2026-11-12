//go:build !windows

package appwin

// brandTaskbar is needed on Windows only: elsewhere the window gets its name
// and icon from a desktop entry (Linux) or the app bundle (macOS).
var brandTaskbar func()
