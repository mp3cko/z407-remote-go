package io.github.mp3cko.z407.core

import java.io.IOException

/**
 * Asks peers (other devices running the app) to disconnect: the speakers
 * accept one controller at a time, and a computer that was left running
 * holds the link. A port of internal/peers/takeover.go.
 *
 * A session ([newSession]: one stay of the app on screen, or one press of
 * Reconnect) begins by asking every peer at once, all at the same time,
 * while the speakers are searched for. A peer added later is asked when the
 * speakers have not been found within the timeout. Every peer is asked at
 * most once per session. An app that was asked to disconnect searches no
 * more until then, so two that list each other cannot keep taking the
 * speakers from each other. And a process that Android keeps for days can
 * still take the speakers over the next time it is opened.
 *
 * Not thread-safe: call its functions from one thread at a time. [post] is
 * called for several peers at the same time.
 */
class Takeover(
    private val peers: () -> List<String>,
    private val connected: () -> Boolean,
    /** Sends POST /api/disconnect to "host:port" and describes the answer. */
    private val post: (peer: String) -> String,
    private val log: (String) -> Unit = {},
    private val timeoutMillis: Long = SEARCH_TIMEOUT_MILLIS,
    /** Whether the connection attempt under way has seen the speakers. */
    private val found: () -> Boolean = { false },
    /** Tells the user, in a few words, what is being done: one note when a round starts, one with its outcome. */
    private val note: (String) -> Unit = {},
    private val foundTimeoutMillis: Long = CONNECT_TIMEOUT_MILLIS,
) {
    companion object {
        /**
         * How long the app searches on its own before asking peers it has
         * not asked yet. Speakers that are free show up within a second or
         * two. The first round of a session does not wait.
         */
        const val SEARCH_TIMEOUT_MILLIS = 3_000L
        /**
         * Replaces the search timeout once the speakers have been seen:
         * setting up the connection takes a moment longer, and a peer must
         * not be closed for that.
         */
        const val CONNECT_TIMEOUT_MILLIS = 10_000L
        /**
         * Bounds the request to one peer; a computer that is off must not
         * hold up the others. The peers are asked at the same time, so a
         * round takes about this long at most.
         */
        const val PEER_TIMEOUT_MILLIS = 2_000
        const val CHECK_INTERVAL_MILLIS = 1_000L

        private const val RELEASED = 0
        private const val REFUSED = 1
        private const val SILENT = 2

        /** "a", "a and b", "a, b and c". */
        private fun joinNames(names: List<String>): String =
            if (names.size == 1) names[0] else names.dropLast(1).joinToString(", ") + " and " + names.last()

        /** One note for a round: who released the speakers, else who would not, else who did not answer. */
        internal fun outcomeText(peers: List<String>, outcome: IntArray): String {
            val by = peers.indices.groupBy({ outcome[it] }, { peers[it] })
            return when {
                by[RELEASED] != null -> joinNames(by.getValue(RELEASED)) + " released the speakers"
                by[REFUSED] != null -> joinNames(by.getValue(REFUSED)) + " did not release the speakers"
                peers.size == 1 -> "${peers[0]} did not answer"
                else -> "No other device answered"
            }
        }
    }

    private var searching = false
    private var since = 0L
    /** Peers dealt with in this session; null = none yet, so the next round is at once. */
    private var asked: HashSet<String>? = null

    /** Starts afresh: the app has come to the screen (again). */
    fun newSession() {
        searching = false
        asked = null
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
        val done = asked
        if (done != null && nowMillis - since < (if (found()) foundTimeoutMillis else timeoutMillis)) return
        val first = done == null
        val set = done ?: HashSet<String>().also { asked = it }
        val round = peers().filter { set.add(it) }
        if (round.isNotEmpty()) askAll(round, first)
    }

    /** Asks [round] at the same time and waits for every answer. */
    private fun askAll(round: List<String>, first: Boolean) {
        note(if (round.size == 1) "Asking ${round[0]} to release the speakers" else "Asking ${round.size} other devices to release the speakers")
        val why = if (first) "new session" else "speakers not found"
        val outcome = IntArray(round.size) { SILENT }
        val lines = arrayOfNulls<String>(round.size)
        val threads = round.mapIndexed { i, peer ->
            Thread({
                try {
                    val answer = post(peer)
                    outcome[i] = if (answer.startsWith("200")) RELEASED else REFUSED
                    lines[i] = "Asked $peer to release the speakers ($why): $answer"
                } catch (e: IOException) {
                    // Normal: that computer is off, or the app is not running there.
                    lines[i] = "Other devices: $peer did not answer (${e.message})"
                } catch (e: Exception) {
                    // Uncaught, it would end the app.
                    lines[i] = "Other devices: could not ask $peer ($e)"
                }
            }, "z407-ask").apply { start() }
        }
        try {
            threads.forEach { it.join() }
        } catch (e: InterruptedException) {
            threads.forEach { it.interrupt() }
            throw e // disconnect(): no outcome to tell
        }
        lines.forEach { if (it != null) log(it) }
        note(outcomeText(round, outcome))
    }
}
