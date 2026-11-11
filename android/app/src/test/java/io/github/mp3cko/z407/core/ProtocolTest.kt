package io.github.mp3cko.z407.core

import org.junit.Assert.assertEquals
import org.junit.Test

/** The same values as internal/protocol/protocol_test.go. */
class ProtocolTest {
    @Test
    fun commandBytes() {
        val cases = mapOf(
            "8405" to Protocol.HANDSHAKE,
            "8400" to Protocol.KEEP_ALIVE_REPLY,
            "8000" to Protocol.BASS_UP,
            "8001" to Protocol.BASS_DOWN,
            "8002" to Protocol.VOLUME_UP,
            "8003" to Protocol.VOLUME_DOWN,
            "8004" to Protocol.PLAY_PAUSE,
            "8005" to Protocol.NEXT_TRACK,
            "8006" to Protocol.PREV_TRACK,
            "8101" to Protocol.INPUT_BLUETOOTH,
            "8102" to Protocol.INPUT_AUX,
            "8103" to Protocol.INPUT_USB,
            "8200" to Protocol.BLUETOOTH_PAIR,
            "8300" to Protocol.FACTORY_RESET,
        )
        for ((want, cmd) in cases) assertEquals(want, hex(cmd))
    }

    @Test
    fun notificationsAndUuids() {
        assertEquals("d40501", hex(Protocol.NOTIFY_KEEP_ALIVE))
        assertEquals("d40001", hex(Protocol.NOTIFY_READY))
        assertEquals("0000fdc2-0000-1000-8000-00805f9b34fb", Protocol.SERVICE_UUID)
        assertEquals("c2e758b9-0e78-41e0-b0cb-98a593193fc5", Protocol.COMMAND_UUID)
        assertEquals("b84ac9c6-29c5-46d4-bba1-9d534784330f", Protocol.RESPONSE_UUID)
    }
}
