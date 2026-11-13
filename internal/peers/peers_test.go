package peers

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	good := map[string]string{
		"living-room-pc":      "living-room-pc:5000",
		"  Desk.Local ":       "desk.local:5000",
		"pc:8080":             "pc:8080",
		"192.168.1.20":        "192.168.1.20:5000",
		"192.168.1.20:5001":   "192.168.1.20:5001",
		"localhost":           "localhost:5000",
		"my_pc":               "my_pc:5000",
		"[fe80::1]":           "[fe80::1]:5000",
		"[fe80::1]:6000":      "[fe80::1]:6000",
		"fe80::1":             "[fe80::1]:5000",
		"pc1.home.example.de": "pc1.home.example.de:5000",
	}
	for in, want := range good {
		got, err := Normalize(in, 5000)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "   ", "http://pc", "https://pc:5000", "pc/remote", "pc:5000/", "user@pc", "two words",
		"pc:", "pc:0", "pc:65536", "pc:http", ":5000", "-pc", "pc-", "pc..local", "a$b",
		"1.2.3", "300.1.1.1", "[192.168.1.1]:5000", "[fe80::1", "[nonsense]", "fe80::zz", "[fe80::1]:x",
		strings.Repeat("a", 64), strings.Repeat("a.", 127) + "com",
	}
	for _, in := range bad {
		if got, err := Normalize(in, 5000); err == nil {
			t.Errorf("%q: accepted as %q", in, got)
		} else if !strings.Contains(err.Error(), "not a valid address") || !strings.Contains(err.Error(), "192.168.1.20:2408") {
			t.Errorf("%q: unhelpful message: %v", in, err)
		}
	}
}

func TestParseList(t *testing.T) {
	got, err := ParseList(" pc , 192.168.1.20:5001,, PC:5000 ,", 5000)
	if err != nil || !reflect.DeepEqual(got, []string{"pc:5000", "192.168.1.20:5001"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, empty := range []string{"", " ", ",,"} {
		if got, err := ParseList(empty, 5000); err != nil || got == nil || len(got) != 0 {
			t.Errorf("%q: got %#v, %v", empty, got, err)
		}
	}
	if _, err := ParseList("pc, bad host", 5000); err == nil || !strings.Contains(err.Error(), `"bad host"`) {
		t.Errorf("error should name the bad entry: %v", err)
	}
	var many []string
	for i := 0; i <= MaxPeers; i++ {
		many = append(many, "pc"+string(rune('a'+i)))
	}
	if _, err := ParseList(strings.Join(many, ","), 5000); err == nil {
		t.Error("too long a list was accepted")
	}
}

func TestStoreSaveAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "z407", "config.json")
	s, err := Open(path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if list, fixed := s.List(); len(list) != 0 || list == nil || fixed {
		t.Fatalf("fresh store: %#v, %v", list, fixed)
	}

	saved, err := s.Save("desk, 192.168.1.20:5001")
	want := []string{"desk:5000", "192.168.1.20:5001"}
	if err != nil || !reflect.DeepEqual(saved, want) {
		t.Fatalf("save: %v, %v", saved, err)
	}
	// In effect at once...
	if list, _ := s.List(); !reflect.DeepEqual(list, want) {
		t.Fatalf("list after save: %v", list)
	}
	// ...and after a restart.
	s2, err := Open(path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if list, fixed := s2.List(); !reflect.DeepEqual(list, want) || fixed {
		t.Fatalf("after reopen: %v, %v", list, fixed)
	}

	// Invalid input changes nothing, neither in memory nor on disk.
	if _, err := s2.Save("desk, http://nope"); err == nil {
		t.Fatal("invalid list saved")
	}
	if list, _ := s2.List(); !reflect.DeepEqual(list, want) {
		t.Fatalf("list after failed save: %v", list)
	}
	if s3, _ := Open(path, 5000); !reflect.DeepEqual(first(s3.List()), want) {
		t.Fatal("file changed by a failed save")
	}

	// The list may be emptied.
	if saved, err := s2.Save(""); err != nil || len(saved) != 0 {
		t.Fatalf("emptying: %v, %v", saved, err)
	}
	if s4, _ := Open(path, 5000); len(first(s4.List())) != 0 {
		t.Fatal("empty list not persisted")
	}
	// No temporary files are left behind.
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("config dir holds %d entries", len(entries))
	}
}

func first(l []string, _ bool) []string { return l }

func TestStoreFixedByFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, _ := Open(path, 5000)
	if _, err := s.Save("saved-pc"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	s, _ = Open(path, 5000)
	if err := s.Fix("flag-pc:6000, other"); err != nil {
		t.Fatal(err)
	}
	if list, fixed := s.List(); !reflect.DeepEqual(list, []string{"flag-pc:6000", "other:5000"}) || !fixed {
		t.Fatalf("list: %v, fixed=%v", list, fixed)
	}
	if _, err := s.Save("something-else"); !errors.Is(err, ErrFixed) {
		t.Fatalf("save while fixed: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("the flag changed the config file:\n%s", after)
	}
	// The next run without the flag sees the saved list again.
	s, _ = Open(path, 5000)
	if list, fixed := s.List(); !reflect.DeepEqual(list, []string{"saved-pc:5000"}) || fixed {
		t.Fatalf("next run: %v, fixed=%v", list, fixed)
	}

	// An empty flag value is a valid, empty list for the run.
	if err := s.Fix(""); err != nil {
		t.Fatal(err)
	}
	if list, fixed := s.List(); len(list) != 0 || !fixed {
		t.Fatalf("empty flag: %v, fixed=%v", list, fixed)
	}
	if err := s.Fix("bad host"); err == nil {
		t.Error("invalid flag value accepted")
	}
}

func TestStoreInMemory(t *testing.T) {
	s, err := Open("", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save("pc"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); !reflect.DeepEqual(list, []string{"pc:5000"}) {
		t.Fatalf("list: %v", list)
	}
}

func TestStoreBadFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"garbage.json": "{not json",
		"invalid.json": `{"peers": ["http://x"]}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := Open(path, 5000)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		// Still usable: starts empty and can be saved over.
		if list, _ := s.List(); len(list) != 0 {
			t.Errorf("%s: list %v", name, list)
		}
		if _, err := s.Save("pc"); err != nil {
			t.Errorf("%s: save: %v", name, err)
		}
	}
	// A directory where the file should be: reading and saving fail cleanly.
	s, err := Open(dir, 5000)
	if err == nil {
		t.Error("opening a directory gave no error")
	}
	if _, err := s.Save("pc"); err == nil || !strings.Contains(err.Error(), "could not save") {
		t.Errorf("save over a directory: %v", err)
	}
	// The config directory cannot be created below a regular file.
	s, _ = Open(filepath.Join(dir, "garbage.json", "sub", "config.json"), 5000)
	if _, err := s.Save("pc"); err == nil {
		t.Error("save below a file succeeded")
	}
}
