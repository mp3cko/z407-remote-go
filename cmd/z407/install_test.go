package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAndUninstallLauncher(t *testing.T) {
	dataHome := filepath.Join(t.TempDir(), "share")
	exe := "/opt/my apps/z407" // a launcher is for Linux, whatever runs the test
	entry, err := installLauncher(dataHome, exe, []byte("png"))
	if err != nil {
		t.Fatal(err)
	}
	wantEntry, wantIcon := launcherFiles(dataHome)
	if entry != wantEntry {
		t.Errorf("entry at %s, want %s", entry, wantEntry)
	}
	text, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"[Desktop Entry]", "Type=Application", "Name=Z407 Remote", "Icon=z407", "Terminal=false",
		`Exec="` + exe + `" --log file`, "StartupWMClass=z407",
	} {
		if !strings.Contains(string(text), line+"\n") {
			t.Errorf("no line %q in:\n%s", line, text)
		}
	}
	if icon, err := os.ReadFile(wantIcon); err != nil || string(icon) != "png" {
		t.Errorf("icon: %q, %v", icon, err)
	}

	// Installing again replaces the files; uninstalling twice is fine.
	if _, err := installLauncher(dataHome, exe, []byte("png")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := uninstallLauncher(dataHome); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{wantEntry, wantIcon} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Errorf("%s is still there (%v)", name, err)
		}
	}
}

func TestExecQuote(t *testing.T) {
	for in, want := range map[string]string{
		`/usr/bin/z407`:   `"/usr/bin/z407"`,
		`/a b/z407`:       `"/a b/z407"`,
		`/a"b/$c/` + "`d": `"/a\"b/\$c/` + "\\`" + `d"`,
		`/100%/z407`:      `"/100%%/z407"`,
		`/back\slash`:     `"/back\\slash"`,
	} {
		if got := execQuote(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestIsAppBundle(t *testing.T) {
	for path, want := range map[string]bool{
		"/Applications/Z407 Remote.app/Contents/MacOS/z407": true,
		"/usr/local/bin/z407":                               false,
		"/Users/me/z407.app/z407":                           false,
	} {
		if got := isAppBundle(path); got != want {
			t.Errorf("%s: got %v", path, got)
		}
	}
}
