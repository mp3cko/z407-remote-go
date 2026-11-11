package io.github.mp3cko.z407.core

import java.io.IOException
import org.junit.Assert.fail

class FakeLink : Link {
    private val writes = ArrayList<ByteArray>()
    @Volatile var writeError: IOException? = null
    @Volatile var closed = 0

    @Synchronized
    override fun write(cmd: ByteArray) {
        writeError?.let { throw it }
        writes.add(cmd.copyOf())
    }

    override fun close() {
        closed++
    }

    @Synchronized
    fun written(): List<String> = writes.map(::hex)
}

/** A dialer whose first [failFirst] dials fail; [prepare] runs inside dial(). */
class FakeDialer(
    private val failFirst: Int = 0,
    private val prepare: (FakeLink, LinkEvents) -> Unit = { _, _ -> },
) : Dialer {
    private val links = ArrayList<FakeLink>()
    private val handlers = ArrayList<LinkEvents>()
    private var dials = 0

    override fun dial(events: LinkEvents): Link {
        val link = synchronized(this) {
            dials++
            if (dials <= failFirst) throw IOException("not found")
            FakeLink().also {
                links.add(it)
                handlers.add(events)
            }
        }
        prepare(link, events)
        return link
    }

    @Synchronized fun dialCount() = dials
    @Synchronized fun link(i: Int): FakeLink? = links.getOrNull(i)
    @Synchronized fun events(i: Int): LinkEvents = handlers[i]
}

class MemoryPeerStore(private var peers: List<String> = emptyList()) : PeerStore {
    override fun load() = peers
    override fun save(peers: List<String>) {
        this.peers = peers
    }
}

fun eventually(what: String, cond: () -> Boolean) {
    val deadline = System.nanoTime() + 3_000_000_000
    while (System.nanoTime() < deadline) {
        if (cond()) return
        Thread.sleep(1)
    }
    fail("timeout waiting for $what")
}

/** A manager running on its own thread with short timings. */
class Running(dialer: Dialer, val log: MutableList<String> = ArrayList()) {
    val manager = SpeakerManager(dialer, retryMillis = 2, heartbeatMillis = 5, log = { synchronized(log) { log.add(it) } })
    private val thread = Thread { manager.run() }.apply { start() }

    fun waitConnected() = eventually("connected") { manager.status().connected }

    fun stop() {
        manager.stop()
        thread.join(3000)
        if (thread.isAlive) fail("run() did not return after stop()")
    }
}
