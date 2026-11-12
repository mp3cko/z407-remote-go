package io.github.mp3cko.z407.core

import java.io.IOException

/**
 * Asks peers (desktop computers running the app) to quit when the speakers
 * cannot be found: the speakers accept one controller at a time, and a
 * computer that was left running holds the link. A port of
 * internal/peers/takeover.go.
 *
 * Every peer is asked at most once per session, a session being one stay of
 * the app on screen ([newSession]). The desktop app, whose instances can shut
 * each other down, asks once per run to rule out a loop; the phone cannot be
 * shut down by anyone, and a process that Android keeps for days must still
 * be able to take the speakers over the next time it is opened.
 *
 * Not thread-safe: call its functions from one thread at a time.
 */
class Takeover(
    private val peers: () -> List<String>,
    private val connected: () -> Boolean,
    /** Sends POST /api/shutdown to "host:port" and describes the answer. */
    private val post: (peer: String) -> String,
    private val log: (String) -> Unit = {},
    private val timeoutMillis: Long = SEARCH_TIMEOUT_MILLIS,
    /** Whether the connection attempt under way has seen the speakers. */
    private val found: () -> Boolean = { false },
    /** Tells the user, in a few words, what is being done. */
    private val note: (String) -> Unit = {},
    private val foundTimeoutMillis: Long = CONNECT_TIMEOUT_MILLIS,
) {
    companion object {
        /**
         * How long the app searches on its own before asking the peers.
         * Speakers that are free show up within a second or two.
         */
        const val SEARCH_TIMEOUT_MILLIS = 3_000L
        /**
         * Replaces the search timeout once the speakers have been seen:
         * setting up the connection takes a moment longer, and a peer must
         * not be closed for that.
         */
        const val CONNECT_TIMEOUT_MILLIS = 10_000L
        /** Bounds the request to one peer; a computer that is off must not hold up the others. */
        const val PEER_TIMEOUT_MILLIS = 2_000
        const val CHECK_INTERVAL_MILLIS = 1_000L
    }

    private var searching = false
    private var since = 0L
    private val asked = HashSet<String>()

    /** Starts afresh: the app has come to the screen (again). */
    fun newSession() {
        searching = false
        asked.clear()
    }

    /** Looks at the state at [nowMillis] (a monotonic clock) and asks the peers when due. */
    fun check(nowMillis: Long) {
        if (connected()) {
            searching = false
            return
        }
        if (!searching) {
            searching = true
            since = nowMillis
        }
        if (nowMillis - since < (if (found()) foundTimeoutMillis else timeoutMillis)) return
        for (peer in peers()) {
            if (!asked.add(peer)) continue
            note("Asking $peer to release the speakers")
            try {
                val answer = post(peer)
                note(if (answer.startsWith("200")) "$peer released the speakers" else "$peer did not release the speakers")
                log("Speakers not found; asked $peer to release them: $answer")
            } catch (e: IOException) {
                // Normal: that computer is off, or the app is not running there.
                log("Other computers: $peer did not answer (${e.message})")
                note("$peer did not answer")
            }
        }
    }
}
