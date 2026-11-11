package io.github.mp3cko.z407.core

/**
 * Logitech Z407 BLE protocol constants. Keep in step with
 * internal/protocol/protocol.go, the single source of these values.
 * Based on https://github.com/freundTech/logi-z407-reverse-engineering
 */
object Protocol {
    const val SERVICE_UUID = "0000fdc2-0000-1000-8000-00805f9b34fb"
    const val COMMAND_UUID = "c2e758b9-0e78-41e0-b0cb-98a593193fc5"
    const val RESPONSE_UUID = "b84ac9c6-29c5-46d4-bba1-9d534784330f"

    val HANDSHAKE = bytes(0x84, 0x05)
    val KEEP_ALIVE_REPLY = bytes(0x84, 0x00)
    val BASS_UP = bytes(0x80, 0x00)
    val BASS_DOWN = bytes(0x80, 0x01)
    val VOLUME_UP = bytes(0x80, 0x02)
    val VOLUME_DOWN = bytes(0x80, 0x03)
    val PLAY_PAUSE = bytes(0x80, 0x04)
    val NEXT_TRACK = bytes(0x80, 0x05)
    val PREV_TRACK = bytes(0x80, 0x06)
    val INPUT_BLUETOOTH = bytes(0x81, 0x01)
    val INPUT_AUX = bytes(0x81, 0x02)
    val INPUT_USB = bytes(0x81, 0x03)
    val BLUETOOTH_PAIR = bytes(0x82, 0x00)
    val FACTORY_RESET = bytes(0x83, 0x00)

    /** Notifications sent by the speaker on RESPONSE_UUID. */
    val NOTIFY_KEEP_ALIVE = bytes(0xd4, 0x05, 0x01)
    val NOTIFY_READY = bytes(0xd4, 0x00, 0x01)

    private fun bytes(vararg b: Int) = ByteArray(b.size) { b[it].toByte() }
}
