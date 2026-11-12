package io.github.mp3cko.z407.core

import java.io.IOException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/** Mirrors internal/speaker/manager_test.go. */
class SpeakerManagerTest {
    private val started = ArrayList<Running>()

    private fun start(dialer: Dialer): Running = Running(dialer).also { started.add(it) }

    @After
    fun stopAll() = started.forEach { it.stop() }

    @Test
    fun handshakeAfterDial() {
        val d = FakeDialer()
        val r = start(d)
        r.waitConnected()
        assertEquals(listOf("8405"), d.link(0)!!.written())
    }

    @Test
    fun commandsFailWhileNotConnected() {
        val m = SpeakerManager(FakeDialer())
        assertThrows(NotConnectedException::class.java) { m.volumeUp() }
        assertEquals(Status(false, 50), m.status())
    }

    @Test
    fun commandBytes() {
        val d = FakeDialer()
        val r = start(d)
        r.waitConnected()
        val m = r.manager
        val cases: List<Pair<() -> Unit, String>> = listOf(
            m::volumeUp to "8002", m::volumeDown to "8003", m::playPause to "8004",
            m::inputBluetooth to "8101", m::inputAux to "8102", m::inputUsb to "8103",
            m::bluetoothPair to "8200", m::factoryReset to "8300",
            m::bassUp to "8000", m::bassDown to "8001", m::nextTrack to "8005", m::prevTrack to "8006",
        )
        for ((fn, _) in cases) fn()
        assertEquals(listOf("8405") + cases.map { it.second }, d.link(0)!!.written())
    }

    @Test
    fun volumeEstimateStepsAndClamps() {
        val r = start(FakeDialer())
        r.waitConnected()
        val m = r.manager
        assertEquals(50, m.status().volume)
        m.volumeUp()
        assertEquals(55, m.status().volume)
        repeat(30) { m.volumeUp() }
        assertEquals(100, m.status().volume)
        repeat(30) { m.volumeDown() }
        assertEquals(0, m.status().volume)
        m.volumeUp()
        assertEquals(5, m.status().volume)
    }

    @Test
    fun volumeEstimateUnchangedWhenWriteFails() {
        val d = FakeDialer()
        val r = start(d)
        r.waitConnected()
        d.link(0)!!.writeError = IOException("gone")
        assertThrows(IOException::class.java) { r.manager.volumeUp() }
        assertEquals(50, r.manager.status().volume)
    }

    @Test
    fun keepAliveIsAnswered() {
        val d = FakeDialer()
        val r = start(d)
        r.waitConnected()
        d.events(0).onNotify(Protocol.NOTIFY_KEEP_ALIVE)
        d.events(0).onNotify(byteArrayOf(1, 2, 3)) // unknown notifications are ignored
        d.events(0).onNotify(Protocol.NOTIFY_KEEP_ALIVE)
        assertEquals(listOf("8405", "8400", "8400"), d.link(0)!!.written())
        assertTrue(r.manager.status().connected)
    }

    @Test
    fun keepAliveDuringDialIsAnsweredAfterTheHandshake() {
        val d = FakeDialer(prepare = { _, events -> events.onNotify(Protocol.NOTIFY_KEEP_ALIVE) })
        val r = start(d)
        r.waitConnected()
        eventually("deferred reply") { d.link(0)!!.written() == listOf("8405", "8400") }
    }

    @Test
    fun readyNotificationMarksConnected() {
        val d = FakeDialer()
        val r = start(d)
        r.waitConnected()
        // A write error drops the state to "not connected"...
        d.link(0)!!.writeError = IOException("busy")
        assertThrows(IOException::class.java) { r.manager.playPause() }
        d.link(0)!!.writeError = null
        // ...and the speaker's ready notification on the same link restores it,
        // unless the manager has already moved on to a new attempt.
        d.events(0).onNotify(Protocol.NOTIFY_READY)
        r.waitConnected()
    }

    @Test
    fun writeErrorReconnects() {
        val d = FakeDialer()
        val r = start(d)
        r.waitConnected()
        d.link(0)!!.writeError = IOException("gone")
        val e = assertThrows(IOException::class.java) { r.manager.playPause() }
        assertTrue(e.message, e.message!!.contains("write 8004"))
        eventually("second dial") { d.dialCount() >= 2 }
        r.waitConnected()
        eventually("old link closed") { d.link(0)!!.closed > 0 }
        assertEquals(listOf("8405"), d.link(1)!!.written())
    }

    @Test
    fun statusTellsWhatIsGoingOn() {
        // Dials keep failing: the problem is named, capitalized for the display.
        val failing = start(FakeDialer(failFirst = Int.MAX_VALUE))
        eventually("problem") { failing.manager.status().problem == "Not found" }
        assertFalse(failing.manager.status().connected)

        // A dial that hangs: first searching, then, once found, connecting.
        val found = java.util.concurrent.CountDownLatch(1)
        val release = java.util.concurrent.CountDownLatch(1)
        val r = start(FakeDialer(prepare = { _, events -> found.await(); events.onFound(); release.await() }))
        eventually("searching") { r.manager.status().phase == SpeakerManager.PHASE_SEARCHING }
        assertFalse(r.manager.found())
        found.countDown()
        eventually("connecting") { r.manager.status().phase == SpeakerManager.PHASE_CONNECTING }
        assertTrue(r.manager.found())
        release.countDown()
        r.waitConnected()
        assertEquals(Status(true, 50), r.manager.status())
    }

    @Test
    fun failedHandshakeIsNamed() {
        val r = start(FakeDialer(prepare = { link, _ -> link.writeError = IOException("nope") }))
        eventually("problem") { r.manager.status().problem == SpeakerManager.PROBLEM_NO_ANSWER }
    }

    @Test
    fun dialFailureRetries() {
        val d = FakeDialer(failFirst = 3)
        val r = start(d)
        r.waitConnected()
        assertEquals(4, d.dialCount())
    }

    @Test
    fun handshakeFailureRetries() {
        var first = true
        val d = FakeDialer(prepare = { link, _ ->
            if (first) {
                first = false
                link.writeError = IOException("nope")
            }
        })
        val r = start(d)
        eventually("second attempt") { d.dialCount() >= 2 }
        r.waitConnected()
        assertEquals(1, d.link(0)!!.closed)
    }

    @Test
    fun disconnectReconnects() {
        val d = FakeDialer()
        val r = start(d)
        r.waitConnected()
        d.events(0).onDisconnect()
        eventually("reconnect") { d.dialCount() >= 2 && r.manager.status().connected }
        // A late event of the old connection must not disturb the new one.
        d.events(0).onDisconnect()
        d.events(0).onNotify(Protocol.NOTIFY_KEEP_ALIVE)
        assertTrue(r.manager.status().connected)
        assertEquals(listOf("8405"), d.link(1)!!.written())
    }

    @Test
    fun disconnectDuringDialIsNotAConnection() {
        var first = true
        val d = FakeDialer(prepare = { _, events ->
            if (first) {
                first = false
                events.onDisconnect()
            }
        })
        val r = start(d)
        eventually("retry after the early drop") { d.dialCount() >= 2 }
        r.waitConnected()
        assertEquals(1, d.link(0)!!.closed)
    }

    @Test
    fun stopClosesTheLink() {
        val d = FakeDialer()
        val r = start(d)
        r.waitConnected()
        r.stop()
        assertEquals(1, d.link(0)!!.closed)
        assertFalse(r.manager.status().connected)
        assertThrows(NotConnectedException::class.java) { r.manager.playPause() }
    }

    @Test
    fun stopInterruptsADialInProgress() {
        val dialing = CountDownLatch(1)
        val d = Dialer {
            dialing.countDown()
            Thread.sleep(60_000) // a scan that finds nothing
            throw IOException("unreachable")
        }
        val r = start(d)
        assertTrue(dialing.await(3, TimeUnit.SECONDS))
        r.stop() // fails the test if run() does not return
    }

    @Test
    fun resumeAllowsANewRun() {
        val d = FakeDialer()
        val r = start(d)
        r.waitConnected()
        r.manager.volumeUp()
        r.stop()

        // Stopped: run() returns at once.
        val idle = Thread { r.manager.run() }.apply { start() }
        idle.join(3000)
        assertFalse(idle.isAlive)
        assertEquals(1, d.dialCount())

        r.manager.resume()
        val again = Thread { r.manager.run() }.apply { start() }
        eventually("reconnected") { r.manager.status().connected }
        assertEquals(55, r.manager.status().volume) // the estimate survives
        r.manager.stop()
        again.join(3000)
        assertFalse(again.isAlive)
    }

    @Test
    fun repeatedFailureIsLoggedOnce() {
        val d = FakeDialer(failFirst = 5)
        val r = start(d)
        r.waitConnected()
        val log = synchronized(r.log) { r.log.toList() }
        assertEquals(1, log.count { it.startsWith("Dial failed: not found") })
        assertEquals(1, log.count { it == "Connected to Z407" })
    }

    @Test
    fun concurrentUse() {
        val d = FakeDialer()
        val r = start(d)
        r.waitConnected()
        val threads = (0 until 8).map { i ->
            Thread {
                repeat(100) {
                    try {
                        if (i % 2 == 0) r.manager.volumeUp() else r.manager.volumeDown()
                    } catch (_: IOException) {
                    }
                    r.manager.status()
                    d.events(0).onNotify(Protocol.NOTIFY_KEEP_ALIVE)
                }
            }.apply { start() }
        }
        threads.forEach { it.join() }
        assertEquals(1 + 800 + 800, d.link(0)!!.written().size)
        assertTrue(r.manager.status().volume in 0..100)
        assertTrue(r.manager.status().connected)
    }
}
