// Package protocol holds the Logitech Z407 BLE protocol constants.
// Based on https://github.com/freundTech/logi-z407-reverse-engineering
package protocol

const (
	ServiceUUID  = "0000fdc2-0000-1000-8000-00805f9b34fb"
	CommandUUID  = "c2e758b9-0e78-41e0-b0cb-98a593193fc5"
	ResponseUUID = "b84ac9c6-29c5-46d4-bba1-9d534784330f"
)

// Command is a raw GATT command payload.
type Command []byte

var (
	Handshake      = Command{0x84, 0x05}
	KeepAliveReply = Command{0x84, 0x00}
	BassUp         = Command{0x80, 0x00}
	BassDown       = Command{0x80, 0x01}
	VolumeUp       = Command{0x80, 0x02}
	VolumeDown     = Command{0x80, 0x03}
	PlayPause      = Command{0x80, 0x04}
	NextTrack      = Command{0x80, 0x05}
	PrevTrack      = Command{0x80, 0x06}
	InputBluetooth = Command{0x81, 0x01}
	InputAux       = Command{0x81, 0x02}
	InputUSB       = Command{0x81, 0x03}
	BluetoothPair  = Command{0x82, 0x00}
	FactoryReset   = Command{0x83, 0x00}
)

// Notifications sent by the speaker on ResponseUUID.
var (
	NotifyKeepAlive = []byte{0xd4, 0x05, 0x01}
	NotifyReady     = []byte{0xd4, 0x00, 0x01}
)
