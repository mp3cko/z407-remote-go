package io.github.mp3cko.z407.core

import org.junit.Assert.assertEquals
import org.junit.Test

class SummaryTest {
    @Test
    fun saysWhatTheRemoteDisplaySays() {
        assertEquals(Summary("Connected", "Volume about 55"), Summary.of(Status(true, 55), released = false))
        assertEquals(
            Summary("Searching…", "Looking for the speakers"),
            Summary.of(Status(false, 50, SpeakerManager.PHASE_SEARCHING), released = false),
        )
        assertEquals(
            Summary("Searching…", "Speakers not found"),
            Summary.of(Status(false, 50, SpeakerManager.PHASE_SEARCHING, "Speakers not found"), released = false),
        )
        assertEquals(
            Summary("Connecting…", "Speakers found"),
            Summary.of(Status(false, 50, SpeakerManager.PHASE_CONNECTING), released = false),
        )
        assertEquals(
            Summary("Not connected", "Bluetooth is off or missing"),
            Summary.of(Status(false, 50, "", "Bluetooth is off or missing"), released = false),
        )
        assertEquals(Summary("Not connected", "Waiting for the next attempt"), Summary.of(Status(false, 50), released = false))
        // Released wins over whatever the manager still reports.
        assertEquals("Disconnected", Summary.of(Status(true, 55), released = true).title)
    }
}
