//go:build linux

package media

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestLinuxKeysyms(t *testing.T) {
	want := map[Key]string{
		PlayPause: "XF86AudioPlay", Next: "XF86AudioNext", Prev: "XF86AudioPrev",
		VolUp: "XF86AudioRaiseVolume", VolDown: "XF86AudioLowerVolume", Mute: "XF86AudioMute",
	}
	for k, sym := range want {
		var got []string
		p := &linuxPresser{run: func(name string, args ...string) error {
			got = append([]string{name}, args...)
			return nil
		}}
		if err := p.Press(k); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if !reflect.DeepEqual(got, []string{"xdotool", "key", sym}) {
			t.Errorf("%s: got %v", k, got)
		}
	}
}

func TestLinuxUnknownKey(t *testing.T) {
	p := &linuxPresser{run: func(string, ...string) error { t.Fatal("should not run"); return nil }}
	if err := p.Press(Key("bogus")); err == nil {
		t.Fatal("expected error")
	}
}

func TestLinuxXdotoolMissing(t *testing.T) {
	p := &linuxPresser{run: func(string, ...string) error { return exec.ErrNotFound }}
	err := p.Press(PlayPause)
	if err == nil || !strings.Contains(err.Error(), "xdotool") || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestLinuxRunFailure(t *testing.T) {
	boom := errors.New("boom")
	p := &linuxPresser{run: func(string, ...string) error { return boom }}
	if err := p.Press(Mute); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}

func TestNewLinux(t *testing.T) {
	if New() == nil {
		t.Fatal("nil presser")
	}
}

func TestNewRealRunNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := New().Press(PlayPause)
	if err == nil || !strings.Contains(err.Error(), "xdotool") {
		t.Fatalf("got %v", err)
	}
}
