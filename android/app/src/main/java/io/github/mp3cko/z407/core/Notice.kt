package io.github.mp3cko.z407.core

/**
 * A short message for the remote's display, such as "Asking
 * living-room-pc:5000 to release the speakers". It goes away by itself.
 * A port of internal/server/notice.go.
 */
class Notice(private val nowMillis: () -> Long = { System.nanoTime() / 1_000_000 }) {
    companion object {
        /** How long a notice stays on the display. */
        const val TIME_MILLIS = 8_000L
    }

    private var text = ""
    private var until = 0L

    /** Shows [text] for [TIME_MILLIS], replacing what was shown. */
    @Synchronized
    fun set(text: String) {
        this.text = text
        until = nowMillis() + TIME_MILLIS
    }

    /** The text to show now, "" when there is none. */
    @Synchronized
    fun get(): String = if (text.isEmpty() || nowMillis() >= until) "" else text
}
