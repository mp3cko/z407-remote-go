package io.github.mp3cko.z407.core

import java.io.IOException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Mirrors internal/peers/takeover_test.go, with a fake clock. */
class TakeoverTest {
    private var now = 1_000_000L
    private var connected = false
    private var peers = listOf("desk:5000")
    private val unreachable = HashSet<String>()
    private val calls = ArrayList<String>()
    private val log = ArrayList<String>()
    private var found = false
    private val notes = ArrayList<String>()
    private val timeout = Takeover.SEARCH_TIMEOUT_MILLIS

    private val takeover = Takeover(
        peers = { peers },
        connected = { connected },
        post = { peer ->
            calls.add(peer)
            if (peer in unreachable) throw IOException("connection refused")
            "200 {\"success\":true}"
        },
        log = { log.add(it) },
        found = { found },
        note = { notes.add(it) },
    )

    private fun advance(millis: Long) {
        now += millis
        takeover.check(now)
    }

    @Test
    fun noCallBeforeTheTimeout() {
        advance(0)
        advance(timeout - 1)
        assertEquals(emptyList<String>(), calls)
        advance(1)
        assertEquals(listOf("desk:5000"), calls)
    }

    @Test
    fun seenSpeakersGetMoreTime() {
        found = true
        advance(0)
        advance(Takeover.CONNECT_TIMEOUT_MILLIS - 1)
        assertEquals(emptyList<String>(), calls)
        advance(1) // seen, but the connection never came
        assertEquals(listOf("desk:5000"), calls)
    }

    @Test
    fun notesTellWhatIsGoingOn() {
        peers = listOf("desk:5000", "attic:5000")
        unreachable.add("attic:5000")
        advance(0)
        advance(timeout)
        assertEquals(
            listOf(
                "Asking desk:5000 to release the speakers", "desk:5000 released the speakers",
                "Asking attic:5000 to release the speakers", "attic:5000 did not answer",
            ),
            notes,
        )
    }

    @Test
    fun noCallWhileConnected() {
        connected = true
        advance(0)
        advance(10 * timeout)
        assertEquals(emptyList<String>(), calls)

        // A drop starts a fresh search: the time spent connected does not count.
        connected = false
        advance(1000)
        advance(timeout - 1000)
        assertEquals(emptyList<String>(), calls)
        // Connecting in time cancels it.
        connected = true
        advance(1000)
        connected = false
        advance(1000)
        advance(timeout - 1000)
        assertEquals(emptyList<String>(), calls)
        advance(1000)
        assertEquals(listOf("desk:5000"), calls)
    }

    @Test
    fun everyPeerIsAskedInOrder() {
        peers = listOf("a:5000", "b:5000", "c:5001")
        advance(0)
        advance(timeout)
        assertEquals(peers, calls)
        assertEquals(3, log.size)
        assertTrue(log[0], log[0].contains("asked a:5000 to release them: 200"))
    }

    @Test
    fun unreachablePeerIsSkipped() {
        peers = listOf("off:5000", "on:5000")
        unreachable.add("off:5000")
        advance(0)
        advance(timeout)
        assertEquals(peers, calls)
        assertTrue(log[0], log[0].contains("off:5000 did not answer"))
    }

    @Test
    fun oncePerSession() {
        peers = listOf("desk:5000", "off:5000")
        unreachable.add("off:5000")
        advance(0)
        advance(timeout)
        advance(timeout)
        advance(3_600_000)
        assertEquals(peers, calls)
        // Not after a later connection loss either.
        connected = true
        advance(60_000)
        connected = false
        advance(1000)
        advance(2 * timeout)
        assertEquals(peers, calls)

        // A peer added on the page while searching is asked on the next check.
        peers = peers + "new:5000"
        advance(1000)
        assertEquals(listOf("desk:5000", "off:5000", "new:5000"), calls)
    }

    @Test
    fun newSessionStartsAfresh() {
        advance(0)
        advance(timeout - 1)
        // The app went to the background and came back much later: the time
        // in between is not search time.
        takeover.newSession()
        advance(3_600_000)
        assertEquals(emptyList<String>(), calls)
        advance(timeout)
        assertEquals(listOf("desk:5000"), calls)
        // In a new session the peers are asked again: the computer may have
        // been started again since.
        takeover.newSession()
        advance(0)
        advance(timeout - 1)
        assertEquals(listOf("desk:5000"), calls)
        advance(1)
        assertEquals(listOf("desk:5000", "desk:5000"), calls)
    }
}
