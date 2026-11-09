//go:build darwin

package media

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics
#import <AppKit/AppKit.h>

// postMediaKey sends a system-defined media key event (what the keyboard's
// F-row media keys produce) to the HID event tap.
static void postMediaKey(int key, int down) {
	int state = down ? 0xa : 0xb;
	NSEvent *e = [NSEvent otherEventWithType:NSEventTypeSystemDefined
	                                location:NSZeroPoint
	                           modifierFlags:(state << 8)
	                               timestamp:0
	                            windowNumber:0
	                                 context:nil
	                                 subtype:8
	                                   data1:((key << 16) | (state << 8))
	                                   data2:-1];
	CGEventPost(kCGHIDEventTap, [e CGEvent]);
}
*/
import "C"

// New returns a Presser that posts macOS media key events. The terminal (or
// app) running z407 may need Accessibility permission in System Settings >
// Privacy & Security for the events to be delivered.
func New() Presser {
	return &darwinPresser{post: func(code int, down bool) error {
		d := 0
		if down {
			d = 1
		}
		C.postMediaKey(C.int(code), C.int(d))
		return nil
	}}
}
