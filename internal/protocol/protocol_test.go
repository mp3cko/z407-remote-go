package protocol

import (
	"encoding/hex"
	"testing"
)

// Hex strings follow the protocol documented by freundTech's reverse
// engineering of the speakers.
func TestCommandBytes(t *testing.T) {
	cases := map[string]Command{
		"8405": Handshake,
		"8400": KeepAliveReply,
		"8000": BassUp,
		"8001": BassDown,
		"8002": VolumeUp,
		"8003": VolumeDown,
		"8004": PlayPause,
		"8005": NextTrack,
		"8006": PrevTrack,
		"8101": InputBluetooth,
		"8102": InputAux,
		"8103": InputUSB,
		"8200": BluetoothPair,
		"8300": FactoryReset,
	}
	for h, c := range cases {
		if got := hex.EncodeToString(c); got != h {
			t.Errorf("%s: got %s", h, got)
		}
	}
}

func TestNotificationsAndUUIDs(t *testing.T) {
	if hex.EncodeToString(NotifyKeepAlive) != "d40501" || hex.EncodeToString(NotifyReady) != "d40001" {
		t.Fatal("notify bytes")
	}
	if ServiceUUID != "0000fdc2-0000-1000-8000-00805f9b34fb" ||
		CommandUUID != "c2e758b9-0e78-41e0-b0cb-98a593193fc5" ||
		ResponseUUID != "b84ac9c6-29c5-46d4-bba1-9d534784330f" {
		t.Fatal("uuid mismatch")
	}
}
