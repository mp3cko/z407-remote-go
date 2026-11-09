//go:build windows

package media

import (
	"errors"
	"reflect"
	"testing"
)

func TestWindowsVK(t *testing.T) {
	want := map[Key]byte{PlayPause: 0xB3, Next: 0xB0, Prev: 0xB1, VolUp: 0xAF, VolDown: 0xAE, Mute: 0xAD}
	for k, vk := range want {
		var calls [][2]uint32
		p := &winPresser{keybdEvent: func(v byte, f uint32) error {
			calls = append(calls, [2]uint32{uint32(v), f})
			return nil
		}}
		if err := p.Press(k); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(calls, [][2]uint32{{uint32(vk), 0}, {uint32(vk), 2}}) {
			t.Errorf("%s: %v", k, calls)
		}
	}
}

func TestWindowsErrors(t *testing.T) {
	p := &winPresser{keybdEvent: func(byte, uint32) error { return errors.New("x") }}
	if p.Press(PlayPause) == nil || p.Press(Key("bogus")) == nil {
		t.Fatal("expected errors")
	}
}
