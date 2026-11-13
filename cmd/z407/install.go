package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mp3cko/z407-remote-anywhere/internal/appwin"
	"github.com/mp3cko/z407-remote-anywhere/internal/server"
)

// A Linux program file cannot carry an icon the way a Windows .exe or a
// macOS .app does: the desktop takes icons from a "desktop entry" that
// points at the program. --install writes that entry and the icon, both of
// which the program carries inside itself, into the user's own data
// directory, so the remote shows up in the applications menu.

// launcherFiles are the two files of the menu entry below the data directory.
func launcherFiles(dataHome string) (entry, icon string) {
	return filepath.Join(dataHome, "applications", "z407.desktop"),
		filepath.Join(dataHome, "icons", "hicolor", "512x512", "apps", "z407.png")
}

// execQuote quotes a path for the Exec key of a desktop entry.
func execQuote(path string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`, `%`, `%%`)
	return `"` + r.Replace(path) + `"`
}

// desktopEntry is the menu entry starting exe. There is no terminal behind
// a menu entry, so the log goes to the file.
func desktopEntry(exe string) string {
	return "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Z407 Remote\n" +
		"Comment=Remote control for the Logitech Z407 speakers\n" +
		"Exec=" + execQuote(exe) + " --log file\n" +
		"Icon=z407\n" +
		"Terminal=false\n" +
		"Categories=AudioVideo;Audio;\n" +
		"StartupWMClass=" + appwin.WindowClass + "\n"
}

// installLauncher writes the menu entry for exe and its icon below dataHome
// and returns the path of the entry.
func installLauncher(dataHome, exe string, icon []byte) (string, error) {
	entry, iconFile := launcherFiles(dataHome)
	for name, data := range map[string][]byte{entry: []byte(desktopEntry(exe)), iconFile: icon} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(name, data, 0o644); err != nil {
			return "", err
		}
	}
	return entry, nil
}

// uninstallLauncher removes what installLauncher wrote. Files that are not
// there are fine.
func uninstallLauncher(dataHome string) error {
	entry, iconFile := launcherFiles(dataHome)
	for _, name := range []string{entry, iconFile} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// userDataHome is $XDG_DATA_HOME, by default ~/.local/share.
func userDataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// runLauncher carries out --install and --uninstall.
func runLauncher(stdout io.Writer, install bool) error {
	if runtime.GOOS != "linux" {
		return errors.New("--install and --uninstall are for Linux: on Windows the program has its icon built in, on macOS the app has")
	}
	dataHome, err := userDataHome()
	if err != nil {
		return err
	}
	if !install {
		if err := uninstallLauncher(dataHome); err != nil {
			return err
		}
		refreshMenu(dataHome)
		fmt.Fprintln(stdout, "Removed Z407 Remote from the applications menu.")
		return nil
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return err
	}
	entry, err := installLauncher(dataHome, exe, server.Icon())
	if err != nil {
		return err
	}
	refreshMenu(dataHome)
	fmt.Fprintf(stdout, "Added Z407 Remote to the applications menu (%s).\n"+
		"It starts %s: run --install again if you move the program.\n", entry, exe)
	return nil
}

// refreshMenu nudges desktops that cache the menu. Most watch the directory
// themselves, so a missing tool is fine.
func refreshMenu(dataHome string) {
	_ = exec.Command("update-desktop-database", filepath.Join(dataHome, "applications")).Run()
}
