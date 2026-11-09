package media

import (
	"errors"
	"reflect"
	"testing"
)

func TestDarwinKeyCodes(t *testing.T) {
	want := map[Key]int{VolUp: 0, VolDown: 1, Mute: 7, PlayPause: 16, Next: 17, Prev: 18}
	if !reflect.DeepEqual(darwinKeyCodes, want) {
		t.Fatalf("got %v", darwinKeyCodes)
	}
}

func TestDarwinPressDownThenUp(t *testing.T) {
	type ev struct {
		code int
		down bool
	}
	var got []ev
	p := &darwinPresser{post: func(c int, d bool) error { got = append(got, ev{c, d}); return nil }}
	if err := p.Press(PlayPause); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []ev{{16, true}, {16, false}}) {
		t.Fatalf("got %v", got)
	}
	if err := p.Press(Key("bogus")); err == nil {
		t.Fatal("unknown key should fail")
	}
}

func TestDarwinPressStopsOnError(t *testing.T) {
	calls := 0
	boom := errors.New("boom")
	p := &darwinPresser{post: func(int, bool) error { calls++; return boom }}
	if err := p.Press(Mute); !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
