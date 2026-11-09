//go:build windows

package media

import "golang.org/x/sys/windows"

var vkCodes = map[Key]byte{
	PlayPause: 0xB3,
	Next:      0xB0,
	Prev:      0xB1,
	VolUp:     0xAF,
	VolDown:   0xAE,
	Mute:      0xAD,
}

const keyEventFKeyUp = 0x0002

type winPresser struct {
	keybdEvent func(vk byte, flags uint32) error
}

// New returns a Presser using user32 keybd_event.
func New() Presser {
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("keybd_event")
	return &winPresser{keybdEvent: func(vk byte, flags uint32) error {
		// keybd_event returns no status; Call's error is only meaningful if the proc is missing.
		if err := proc.Find(); err != nil {
			return err
		}
		proc.Call(uintptr(vk), 0, uintptr(flags), 0)
		return nil
	}}
}

func (p *winPresser) Press(k Key) error {
	vk, ok := vkCodes[k]
	if !ok {
		return unknownKey(k)
	}
	if err := p.keybdEvent(vk, 0); err != nil {
		return err
	}
	return p.keybdEvent(vk, keyEventFKeyUp)
}
