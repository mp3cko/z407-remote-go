// Package appwin opens the remote in a browser window: a Chromium "app"
// window when Edge, Chrome, Chromium or Brave is installed, otherwise a tab
// in the system default browser. It only starts processes, so it stays pure Go.
package appwin

import (
	"errors"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"
)

var (
	// ErrNoBrowser means no Chromium-based browser was found.
	ErrNoBrowser = errors.New("no Chromium-based browser found")
	// ErrNoDisplay means there is no graphical session to open a window in.
	ErrNoDisplay = errors.New("no graphical display available")
)

// Window size of the app window: the remote's portrait layout.
const (
	WindowWidth  = 500
	WindowHeight = 940
)

// WindowClass is the window class of the app window on Linux. A desktop
// entry naming it (StartupWMClass) lends the window its icon.
const WindowClass = "z407"

// Process is a started browser.
type Process interface {
	// Wait blocks until the process exits.
	Wait() error
	// Close asks the browser to quit, which closes its window.
	Close() error
}

// System is the part of the operating system this package uses. Tests
// replace the fields; Real returns the live one.
type System struct {
	GOOS     string
	Getenv   func(string) string
	Exists   func(path string) bool
	LookPath func(name string) (string, error)
	CacheDir func() (string, error)
	HomeDir  func() (string, error)
	Start    func(path string, args []string) (Process, error)
}

// Real returns the System backed by the running machine.
func Real(goos string) System {
	return System{
		GOOS:   goos,
		Getenv: os.Getenv,
		Exists: func(p string) bool {
			fi, err := os.Stat(p)
			return err == nil && !fi.IsDir()
		},
		LookPath: exec.LookPath,
		CacheDir: os.UserCacheDir,
		HomeDir:  os.UserHomeDir,
		Start:    startProcess,
	}
}

type osProcess struct{ cmd *exec.Cmd }

func (p osProcess) Wait() error { return p.cmd.Wait() }

// Close asks politely where the system can (SIGTERM lets the browser save its
// profile) and kills otherwise: Windows has no signal to send.
func (p osProcess) Close() error {
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}

func startProcess(path string, args []string) (Process, error) {
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return osProcess{cmd}, nil
}

// HasDisplay reports whether a window can be shown. Only Linux and the BSDs
// can run without a graphical session.
func (s System) HasDisplay() bool {
	if s.GOOS == "windows" || s.GOOS == "darwin" {
		return true
	}
	return s.Getenv("DISPLAY") != "" || s.Getenv("WAYLAND_DISPLAY") != ""
}

// windowsBrowsers are paths below the Program Files and LocalAppData roots.
var windowsBrowsers = []string{
	`Microsoft\Edge\Application\msedge.exe`,
	`Google\Chrome\Application\chrome.exe`,
	`Chromium\Application\chrome.exe`,
	`BraveSoftware\Brave-Browser\Application\brave.exe`,
}

// macBrowsers are paths below /Applications and ~/Applications.
var macBrowsers = []string{
	"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	"Google Chrome.app/Contents/MacOS/Google Chrome",
	"Chromium.app/Contents/MacOS/Chromium",
	"Brave Browser.app/Contents/MacOS/Brave Browser",
}

// pathBrowsers are executable names looked up in PATH (Linux and the BSDs).
var pathBrowsers = []string{
	"microsoft-edge", "microsoft-edge-stable",
	"google-chrome", "google-chrome-stable",
	"chromium", "chromium-browser",
	"brave-browser", "brave",
}

// FindBrowser returns the path of the first installed Chromium-based browser,
// in the order Edge, Chrome, Chromium, Brave.
func (s System) FindBrowser() (string, bool) {
	switch s.GOOS {
	case "windows":
		for _, rel := range windowsBrowsers {
			for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
				root := s.Getenv(env)
				if root == "" {
					continue
				}
				// Not filepath.Join: the separator must be right when tested on other systems.
				if p := strings.TrimRight(root, `\`) + `\` + rel; s.Exists(p) {
					return p, true
				}
			}
		}
	case "darwin":
		roots := []string{"/Applications"}
		if home, err := s.HomeDir(); err == nil && home != "" {
			roots = append(roots, home+"/Applications")
		}
		for _, rel := range macBrowsers {
			for _, root := range roots {
				if p := root + "/" + rel; s.Exists(p) {
					return p, true
				}
			}
		}
	default:
		for _, name := range pathBrowsers {
			if p, err := s.LookPath(name); err == nil && p != "" {
				return p, true
			}
		}
	}
	return "", false
}

// AppArgs returns the browser arguments for an app window showing url. The
// dedicated profile makes the started process the browser itself: with the
// user's normal profile it would hand the URL to the running browser and exit
// at once, so its exit would say nothing about the window.
func AppArgs(url, profileDir string) []string {
	return []string{
		"--app=" + url,
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-mode",   // exit with the window, never linger in the background
		"--hide-crash-restore-bubble", // no "restore pages?" prompt after the app closed the window
		"--window-size=" + strconv.Itoa(WindowWidth) + "," + strconv.Itoa(WindowHeight),
	}
}

// profileDir is the dedicated browser profile for the instance on port. Each
// port gets its own, so a second instance does not join the first one's
// browser process.
func (s System) profileDir(browser string, port int) (string, error) {
	name := "app-window-" + strconv.Itoa(port)
	// A snap-confined Chromium cannot write to hidden directories in $HOME.
	if s.GOOS == "linux" && strings.HasPrefix(browser, "/snap/") {
		home, err := s.HomeDir()
		if err != nil {
			return "", err
		}
		return path.Join(home, "snap", "chromium", "common", "z407", name), nil
	}
	cache, err := s.CacheDir()
	if err != nil {
		return "", err
	}
	// Joined by hand, not with filepath: the separator has to follow GOOS,
	// so that the tests mean the same on every system.
	if s.GOOS == "windows" {
		return strings.TrimRight(cache, `\`) + `\z407\` + name, nil
	}
	return path.Join(cache, "z407", name), nil
}

// OpenApp opens url in a Chromium app window and returns the browser process;
// the window is closed when that process exits. It fails with ErrNoDisplay
// or ErrNoBrowser when there is nothing to open it with.
func (s System) OpenApp(url string, port int) (Process, error) {
	if !s.HasDisplay() {
		return nil, ErrNoDisplay
	}
	browser, ok := s.FindBrowser()
	if !ok {
		return nil, ErrNoBrowser
	}
	dir, err := s.profileDir(browser, port)
	if err != nil {
		return nil, err
	}
	args := AppArgs(url, dir)
	if s.GOOS != "windows" && s.GOOS != "darwin" {
		args = append(args, "--class="+WindowClass)
	}
	return s.Start(browser, args)
}

// DefaultBrowserCommand returns the command that opens url in the system
// default browser.
func DefaultBrowserCommand(goos, url string) (string, []string) {
	switch goos {
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		return "open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}

// OpenDefault opens url in the system default browser. The helper process
// exits right away, so nothing can be learned from it about the page.
func (s System) OpenDefault(url string) error {
	if !s.HasDisplay() {
		return ErrNoDisplay
	}
	name, args := DefaultBrowserCommand(s.GOOS, url)
	p, err := s.Start(name, args)
	if err != nil {
		return err
	}
	go func() { _ = p.Wait() }() // reap the helper
	return nil
}
