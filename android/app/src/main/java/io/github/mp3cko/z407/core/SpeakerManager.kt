package io.github.mp3cko.z407.core

import java.io.IOException

/** Thrown by commands when the speakers are not connected. */
class NotConnectedException : IOException("speakers not connected")

/**
 * Connection state and estimated volume (0-100). While not connected,
 * [connecting] tells whether an attempt is under way and [problem] what went
 * wrong with the last one ("" when nothing did).
 */
data class Status(
    val connected: Boolean,
    val volume: Int,
    val connecting: Boolean = false,
    val problem: String = "",
)

/**
 * Owns the connection lifecycle and the command API. A port of
 * internal/speaker/manager.go, kept free of Android classes so it runs in
 * JVM unit tests. Safe for concurrent use.
 */
class SpeakerManager(
    private val dialer: Dialer,
    private val retryMillis: Long = DEFAULT_RETRY_MILLIS,
    private val heartbeatMillis: Long = DEFAULT_HEARTBEAT_MILLIS,
    private val log: (String) -> Unit = {},
) {
    companion object {
        const val DEFAULT_RETRY_MILLIS = 5000L
        const val DEFAULT_HEARTBEAT_MILLIS = 5000L
        const val VOLUME_STEP = 5
        const val PROBLEM_CONNECT = "Could not connect"
        const val PROBLEM_NO_ANSWER = "Speakers did not answer"
        const val PROBLEM_LOST = "Connection lost"
    }

    private val lock = Object()
    private var link: Link? = null
    private var connected = false
    private var connecting = false // a connection attempt is under way
    private var problem = "" // what went wrong with the last attempt
    private var dropped = false // current attempt lost its connection
    private var pendingKeepAlive = false // keep-alive received before dial returned
    private var attempt = 0 // generation of the current attempt; stale callbacks are ignored
    private var volume = 50
    private var wake = false // the current connection was lost
    private var stopped = false
    private var runner: Thread? = null
    private var lastFail = "" // only touched by the thread in run()

    /**
     * Keeps the speakers connected until [stop] is called, then closes the
     * link. Blocks; call it on a thread of its own.
     */
    fun run() {
        synchronized(lock) {
            if (stopped) return
            runner = Thread.currentThread()
        }
        try {
            while (!isStopped()) {
                if (status().connected) {
                    pause(heartbeatMillis, wakeable = true)
                } else if (!connectOnce()) {
                    pause(retryMillis, wakeable = false)
                }
            }
        } finally {
            shutdown()
        }
    }

    /** Allows [run] again after a [stop]. Call it before starting the thread that runs. */
    fun resume() {
        synchronized(lock) { stopped = false }
    }

    /** Makes [run] return, now or as soon as it is called, until [resume]. */
    fun stop() {
        synchronized(lock) {
            stopped = true
            runner?.interrupt() // a dial in progress is abandoned
            lock.notifyAll()
        }
    }

    private fun isStopped() = synchronized(lock) { stopped }

    private fun pause(millis: Long, wakeable: Boolean) {
        val deadline = System.nanoTime() + millis * 1_000_000
        synchronized(lock) {
            while (!stopped && !(wakeable && wake)) {
                val left = (deadline - System.nanoTime()) / 1_000_000
                if (left <= 0) break
                try {
                    lock.wait(left)
                } catch (_: InterruptedException) {
                    // stop() interrupts; the loop condition sees it
                }
            }
        }
    }

    private fun shutdown() {
        val old: Link?
        synchronized(lock) {
            old = link
            link = null
            connected = false
            connecting = false
            attempt++
            runner = null
        }
        Thread.interrupted() // clear the flag so close() is not cut short
        old?.close()
    }

    private fun logFail(msg: String) {
        if (msg == lastFail) return // absent speakers must not flood the log
        lastFail = msg
        log("$msg (retrying every ${retryMillis / 1000.0}s, repeats hidden)")
    }

    /** Makes one dial + handshake attempt and reports success. */
    private fun connectOnce(): Boolean {
        val old: Link?
        val id: Int
        synchronized(lock) {
            old = link
            link = null
            connected = false
            dropped = false
            pendingKeepAlive = false
            connecting = true
            wake = false // discard wake-ups belonging to the previous connection
            id = ++attempt
        }
        old?.close()

        val l: Link
        try {
            l = dialer.dial(object : LinkEvents {
                override fun onNotify(data: ByteArray) = this@SpeakerManager.onNotify(id, data)
                override fun onDisconnect() = this@SpeakerManager.onDisconnect(id)
            })
        } catch (e: InterruptedException) {
            Thread.currentThread().interrupt()
            synchronized(lock) { connecting = false }
            return false
        } catch (e: IOException) {
            logFail("Dial failed: ${e.message}")
            // The dialer's messages are short and plain ("Bluetooth is switched off").
            failed(e.message?.replaceFirstChar { it.uppercase() } ?: PROBLEM_CONNECT)
            return false
        }
        try {
            l.write(Protocol.HANDSHAKE)
        } catch (e: IOException) {
            logFail("Handshake failed: ${e.message}")
            l.close()
            failed(PROBLEM_NO_ANSWER)
            return false
        }

        var accepted = false
        var pending = false
        synchronized(lock) {
            if (attempt == id && !dropped && !stopped) {
                link = l
                connected = true
                pending = pendingKeepAlive
                pendingKeepAlive = false
                accepted = true
                problem = ""
            } else {
                problem = PROBLEM_LOST
            }
            connecting = false
        }
        if (!accepted) {
            l.close()
            return false
        }

        if (pending) reply(l)
        lastFail = ""
        log("Connected to Z407")
        return true
    }

    /** Ends a connection attempt that did not work out. */
    private fun failed(why: String) {
        synchronized(lock) {
            connecting = false
            problem = why
        }
    }

    private fun reply(l: Link) {
        try {
            l.write(Protocol.KEEP_ALIVE_REPLY)
        } catch (_: IOException) {
            fail(l)
        }
    }

    private fun onNotify(id: Int, data: ByteArray) {
        var answerOn: Link? = null
        synchronized(lock) {
            if (id != attempt) return
            when {
                data.contentEquals(Protocol.NOTIFY_KEEP_ALIVE) -> {
                    answerOn = link
                    if (answerOn == null) pendingKeepAlive = true
                }
                data.contentEquals(Protocol.NOTIFY_READY) -> {
                    connected = true
                    dropped = false
                }
            }
        }
        answerOn?.let { reply(it) }
    }

    private fun onDisconnect(id: Int) {
        synchronized(lock) {
            if (id == attempt) {
                connected = false
                dropped = true
                signal()
            }
        }
    }

    /** Marks the connection over [l] as lost. */
    private fun fail(l: Link) {
        synchronized(lock) {
            if (link === l) {
                connected = false
                signal()
            }
        }
    }

    /** Must be called with [lock] held. */
    private fun signal() {
        wake = true
        lock.notifyAll()
    }

    fun status(): Status = synchronized(lock) {
        if (connected) Status(true, volume) else Status(false, volume, connecting, problem)
    }

    @Throws(IOException::class)
    private fun send(cmd: ByteArray) {
        val l = synchronized(lock) { if (connected) link else null } ?: throw NotConnectedException()
        try {
            l.write(cmd)
        } catch (e: IOException) {
            fail(l)
            throw IOException("write ${hex(cmd)}: ${e.message}", e)
        }
    }

    @Throws(IOException::class)
    private fun adjustVolume(cmd: ByteArray, delta: Int) {
        send(cmd)
        synchronized(lock) { volume = (volume + delta).coerceIn(0, 100) }
    }

    @Throws(IOException::class) fun volumeUp() = adjustVolume(Protocol.VOLUME_UP, VOLUME_STEP)
    @Throws(IOException::class) fun volumeDown() = adjustVolume(Protocol.VOLUME_DOWN, -VOLUME_STEP)
    @Throws(IOException::class) fun playPause() = send(Protocol.PLAY_PAUSE)
    @Throws(IOException::class) fun inputBluetooth() = send(Protocol.INPUT_BLUETOOTH)
    @Throws(IOException::class) fun inputAux() = send(Protocol.INPUT_AUX)
    @Throws(IOException::class) fun inputUsb() = send(Protocol.INPUT_USB)
    @Throws(IOException::class) fun bluetoothPair() = send(Protocol.BLUETOOTH_PAIR)
    @Throws(IOException::class) fun factoryReset() = send(Protocol.FACTORY_RESET)
    @Throws(IOException::class) fun bassUp() = send(Protocol.BASS_UP)
    @Throws(IOException::class) fun bassDown() = send(Protocol.BASS_DOWN)
    @Throws(IOException::class) fun nextTrack() = send(Protocol.NEXT_TRACK)
    @Throws(IOException::class) fun prevTrack() = send(Protocol.PREV_TRACK)
}

fun hex(b: ByteArray): String = b.joinToString("") { "%02x".format(it.toInt() and 0xff) }
