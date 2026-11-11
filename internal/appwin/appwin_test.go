package appwin

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type fakeProc struct{ done chan struct{} }

func (p fakeProc) Wait() error  { <-p.done; return nil }
func (p fakeProc) Close() error { return nil }

type started struct {
	path string
	args []string
}

// fakeSystem builds a System whose files and PATH entries are the given sets.
func fakeSystem(goos string, env map[string]string, files []string, path map[string]string) (System, *[]started) {
	var log []started
	return System{
		GOOS:   goos,
		Getenv: func(k string) string { return env[k] },
		Exists: func(p string) bool { return slices.Contains(files, p) },
		LookPath: func(n string) (string, error) {
			if p, ok := path[n]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		CacheDir: func() (string, error) { return env["CACHE"], nil },
		HomeDir:  func() (string, error) { return env["HOME"], nil },
		Start: func(p string, a []string) (Process, error) {
			log = append(log, started{p, a})
			done := make(chan struct{})
			close(done)
			return fakeProc{done}, nil
		},
	}, &log
}

var winEnv = map[string]string{
	"ProgramFiles":      `C:\Program Files`,
	"ProgramFiles(x86)": `C:\Program Files (x86)`,
	"LocalAppData":      `C:\Users\me\AppData\Local`,
	"CACHE":             `C:\Users\me\AppData\Local`,
}

func TestFindBrowser(t *testing.T) {
	edge := `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`
	chrome := `C:\Program Files\Google\Chrome\Application\chrome.exe`
	userChrome := `C:\Users\me\AppData\Local\Google\Chrome\Application\chrome.exe`
	brave := `C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`
	macChrome := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	macUserBrave := "/Users/me/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"
	cases := []struct {
		name  string
		goos  string
		env   map[string]string
		files []string
		path  map[string]string
		want  string
	}{
		{"windows prefers Edge", "windows", winEnv, []string{chrome, edge}, nil, edge},
		{"windows Chrome", "windows", winEnv, []string{brave, chrome}, nil, chrome},
		{"windows per-user Chrome", "windows", winEnv, []string{userChrome}, nil, userChrome},
		{"windows Brave", "windows", winEnv, []string{brave}, nil, brave},
		{"windows none", "windows", winEnv, nil, nil, ""},
		{"windows without env", "windows", nil, []string{edge}, nil, ""},
		{"mac Chrome", "darwin", map[string]string{"HOME": "/Users/me"}, []string{macUserBrave, macChrome}, nil, macChrome},
		{"mac user Applications", "darwin", map[string]string{"HOME": "/Users/me"}, []string{macUserBrave}, nil, macUserBrave},
		{"mac none", "darwin", nil, nil, nil, ""},
		{"linux prefers Edge", "linux", nil, nil, map[string]string{"chromium": "/usr/bin/chromium", "microsoft-edge": "/usr/bin/microsoft-edge"}, "/usr/bin/microsoft-edge"},
		{"linux Chrome stable", "linux", nil, nil, map[string]string{"google-chrome-stable": "/usr/bin/google-chrome-stable", "brave-browser": "/usr/bin/brave-browser"}, "/usr/bin/google-chrome-stable"},
		{"linux Brave", "linux", nil, nil, map[string]string{"brave-browser": "/usr/bin/brave-browser"}, "/usr/bin/brave-browser"},
		{"linux none", "linux", nil, nil, map[string]string{"firefox": "/usr/bin/firefox"}, ""},
	}
	for _, c := range cases {
		s, _ := fakeSystem(c.goos, c.env, c.files, c.path)
		got, ok := s.FindBrowser()
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, ok, c.want)
		}
	}
}

func TestAppArgs(t *testing.T) {
	got := AppArgs("http://127.0.0.1:5000", "/cache/z407/app-window-5000")
	want := []string{
		"--app=http://127.0.0.1:5000",
		"--user-data-dir=/cache/z407/app-window-5000",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-mode",
		"--hide-crash-restore-bubble",
		"--window-size=500,940",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if WindowHeight <= WindowWidth {
		t.Error("the window should be portrait")
	}
}

func TestOpenApp(t *testing.T) {
	linux := map[string]string{"DISPLAY": ":0", "CACHE": "/home/me/.cache", "HOME": "/home/me"}
	s, log := fakeSystem("linux", linux, nil, map[string]string{"google-chrome": "/usr/bin/google-chrome"})
	if _, err := s.OpenApp("http://127.0.0.1:5000", 5000); err != nil {
		t.Fatal(err)
	}
	if len(*log) != 1 || (*log)[0].path != "/usr/bin/google-chrome" ||
		!reflect.DeepEqual((*log)[0].args, AppArgs("http://127.0.0.1:5000", "/home/me/.cache/z407/app-window-5000")) {
		t.Fatalf("started %+v", *log)
	}

	// Another port uses another profile, so two instances do not share a browser process.
	if _, err := s.OpenApp("http://127.0.0.1:5001", 5001); err != nil {
		t.Fatal(err)
	}
	if got := (*log)[1].args[1]; got != "--user-data-dir=/home/me/.cache/z407/app-window-5001" {
		t.Errorf("profile: %s", got)
	}

	s, log = fakeSystem("windows", winEnv, []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`}, nil)
	if _, err := s.OpenApp("http://127.0.0.1:5000", 5000); err != nil {
		t.Fatal(err)
	}
	if got := (*log)[0].args[1]; got != `--user-data-dir=C:\Users\me\AppData\Local\z407\app-window-5000` {
		t.Errorf("windows profile: %s", got)
	}

	// Snap Chromium may only write below ~/snap.
	s, log = fakeSystem("linux", linux, nil, map[string]string{"chromium": "/snap/bin/chromium"})
	if _, err := s.OpenApp("http://127.0.0.1:5000", 5000); err != nil {
		t.Fatal(err)
	}
	if got := (*log)[0].args[1]; got != "--user-data-dir=/home/me/snap/chromium/common/z407/app-window-5000" {
		t.Errorf("snap profile: %s", got)
	}
}

func TestOpenAppFailures(t *testing.T) {
	s, log := fakeSystem("linux", map[string]string{"DISPLAY": ":0"}, nil, nil)
	if _, err := s.OpenApp("http://x", 1); !errors.Is(err, ErrNoBrowser) {
		t.Errorf("no browser: %v", err)
	}
	s, _ = fakeSystem("linux", nil, nil, map[string]string{"chromium": "/usr/bin/chromium"})
	if _, err := s.OpenApp("http://x", 1); !errors.Is(err, ErrNoDisplay) {
		t.Errorf("no display: %v", err)
	}
	if err := s.OpenDefault("http://x"); !errors.Is(err, ErrNoDisplay) {
		t.Errorf("default without display: %v", err)
	}
	if len(*log) != 0 {
		t.Errorf("nothing should have started: %+v", *log)
	}

	boom := errors.New("boom")
	s, _ = fakeSystem("linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, nil, map[string]string{"chromium": "/usr/bin/chromium"})
	s.CacheDir = func() (string, error) { return "", boom }
	if _, err := s.OpenApp("http://x", 1); !errors.Is(err, boom) {
		t.Errorf("cache dir error: %v", err)
	}
	s.CacheDir = func() (string, error) { return "/c", nil }
	s.Start = func(string, []string) (Process, error) { return nil, boom }
	if _, err := s.OpenApp("http://x", 1); !errors.Is(err, boom) {
		t.Errorf("start error: %v", err)
	}
	if err := s.OpenDefault("http://x"); !errors.Is(err, boom) {
		t.Errorf("default start error: %v", err)
	}
}

func TestOpenDefault(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": "rundll32 url.dll,FileProtocolHandler http://127.0.0.1:5000",
		"darwin":  "open http://127.0.0.1:5000",
		"linux":   "xdg-open http://127.0.0.1:5000",
	} {
		s, log := fakeSystem(goos, map[string]string{"DISPLAY": ":0"}, nil, nil)
		if err := s.OpenDefault("http://127.0.0.1:5000"); err != nil {
			t.Fatal(err)
		}
		if got := (*log)[0].path + " " + strings.Join((*log)[0].args, " "); got != want {
			t.Errorf("%s: %s", goos, got)
		}
	}
}

func TestHasDisplay(t *testing.T) {
	for _, c := range []struct {
		goos string
		env  map[string]string
		want bool
	}{
		{"windows", nil, true},
		{"darwin", nil, true},
		{"linux", nil, false},
		{"linux", map[string]string{"DISPLAY": ":0"}, true},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, true},
		{"freebsd", nil, false},
	} {
		s, _ := fakeSystem(c.goos, c.env, nil, nil)
		if got := s.HasDisplay(); got != c.want {
			t.Errorf("%s %v: %v", c.goos, c.env, got)
		}
	}
}

func TestReal(t *testing.T) {
	s := Real("linux")
	if s.Exists("/definitely/not/here") || s.Exists("/") {
		t.Error("Exists must be true for regular files only")
	}
	if _, err := s.Start("/definitely/not/here", nil); err == nil {
		t.Error("starting a missing program should fail")
	}
	sleep, err := s.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep command")
	}
	p, err := s.Start(sleep, []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err == nil {
		t.Error("a closed process should report how it ended")
	}
}
