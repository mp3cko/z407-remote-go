package io.github.mp3cko.z407.core

import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** One remembered log line; [n] counts the lines from 1. */
data class LogEntry(val n: Int, val time: String, val level: String, val text: String)

/**
 * Remembers the last lines logged, for the log viewer of the remote. A port
 * of internal/logx/history.go. Safe for concurrent use.
 */
class LogHistory(private val max: Int = 500, private val now: () -> Date = { Date() }) {
    companion object {
        const val INFO = "info"
        const val OK = "ok"
        const val ERROR = "error"
    }

    private val entries = ArrayDeque<LogEntry>()
    private var last = 0
    private val clock = SimpleDateFormat("HH:mm:ss", Locale.US)

    @Synchronized
    fun add(level: String, text: String) {
        entries.addLast(LogEntry(++last, clock.format(now()), level, text))
        while (entries.size > max) entries.removeFirst()
    }

    /**
     * The remembered lines after line number [after] (0 = all of them) and
     * the number of the newest line, which is the next "after".
     */
    @Synchronized
    fun since(after: Int): Pair<List<LogEntry>, Int> = entries.filter { it.n > after } to last
}
