package media

// NX_KEYTYPE_* codes from IOKit's ev_keymap.h, used by macOS system-defined
// media key events. Kept build-tag free so the mapping is unit-tested on any OS.
var darwinKeyCodes = map[Key]int{
	VolUp:     0,  // NX_KEYTYPE_SOUND_UP
	VolDown:   1,  // NX_KEYTYPE_SOUND_DOWN
	Mute:      7,  // NX_KEYTYPE_MUTE
	PlayPause: 16, // NX_KEYTYPE_PLAY
	Next:      17, // NX_KEYTYPE_NEXT
	Prev:      18, // NX_KEYTYPE_PREVIOUS
}

// darwinPresser posts key down + key up through post.
type darwinPresser struct {
	post func(code int, down bool) error
}

func (p *darwinPresser) Press(k Key) error {
	code, ok := darwinKeyCodes[k]
	if !ok {
		return unknownKey(k)
	}
	if err := p.post(code, true); err != nil {
		return err
	}
	return p.post(code, false)
}
