package io.github.mp3cko.z407.core

/**
 * The "other computers" list: validation identical to
 * internal/peers/peers.go, so the page behaves the same in the app as on
 * the desktop.
 */
object Peers {
    /** The desktop app's default port, assumed for a peer given without one. */
    const val DEFAULT_PORT = 2407
    const val MAX_PEERS = 16

    class InvalidPeerException(message: String) : Exception(message)

    /** Validates one entry, "host" or "host:port", and returns it as "host:port". */
    @Throws(InvalidPeerException::class)
    fun normalize(raw: String, defaultPort: Int = DEFAULT_PORT): String {
        val entry = raw.trim()
        fun bad(why: String): Nothing = throw InvalidPeerException(
            "\"$entry\" is not a valid address ($why). Use a name or IP address, " +
                "optionally with a port: living-room-pc or 192.168.1.20:2408"
        )
        if (entry.isEmpty()) bad("it is empty")
        if (entry.contains("://") || entry.any { it in "/?#@ " }) bad("leave out http:// and any path")

        var host = entry
        var port = defaultPort.toString()
        val colons = entry.count { it == ':' }
        when {
            entry.startsWith("[") -> { // [IPv6] or [IPv6]:port
                val close = entry.indexOf(']')
                if (close < 0) bad("the brackets or the port are wrong")
                host = entry.substring(1, close)
                val rest = entry.substring(close + 1)
                if (rest.isNotEmpty()) {
                    if (!rest.startsWith(":")) bad("the brackets or the port are wrong")
                    port = rest.substring(1)
                }
                if (!isIPv6(host)) bad("the brackets must hold an IPv6 address")
            }
            colons == 1 -> {
                host = entry.substringBefore(':')
                port = entry.substringAfter(':')
            }
            colons > 1 -> if (!isIPv6(entry)) bad("write IPv6 addresses as [address]:port")
        }
        val n = if (port.isNotEmpty() && port.length <= 5 && port.all { it in '0'..'9' }) port.toInt() else -1
        if (n < 1 || n > 65535) bad("the port must be a number from 1 to 65535")
        if (!isIPv4(host) && !isIPv6(host) && !validHostname(host)) {
            bad("the name may only contain letters, digits, hyphens and dots")
        }
        val h = host.lowercase()
        return if (h.contains(':')) "[$h]:$n" else "$h:$n"
    }

    private fun isIPv4(s: String): Boolean {
        val parts = s.split('.')
        return parts.size == 4 && parts.all { p ->
            p.isNotEmpty() && p.length <= 3 && p.all { it in '0'..'9' } && p.toInt() <= 255
        }
    }

    /** A structural check: enough to tell an IPv6 literal from a host name. */
    private fun isIPv6(s: String): Boolean {
        if (s.count { it == ':' } < 2 || s.contains(":::")) return false
        val double = s.indexOf("::")
        if (double >= 0 && s.indexOf("::", double + 1) >= 0) return false
        val groups = s.split(':')
        if (double < 0 && groups.size != 8) return false
        if (groups.size > 8) return false
        groups.forEachIndexed { i, g ->
            val edge = (i == 0 || i == groups.size - 1)
            if (g.isEmpty()) {
                if (double < 0 || (edge && !(s.startsWith("::") || s.endsWith("::")))) return false
            } else if (g.length > 4 || !g.all { it in '0'..'9' || it.lowercaseChar() in 'a'..'f' }) {
                return false
            }
        }
        return true
    }

    private fun validHostname(h: String): Boolean {
        if (h.isEmpty() || h.length > 253) return false
        var digitsOnly = true
        for (label in h.split('.')) {
            if (label.isEmpty() || label.length > 63 || label.first() == '-' || label.last() == '-') return false
            for (c in label) {
                when {
                    c in '0'..'9' -> {}
                    c in 'a'..'z' || c in 'A'..'Z' || c == '-' || c == '_' -> digitsOnly = false
                    else -> return false
                }
            }
        }
        return !digitsOnly // "1.2.3" or "300.1.1.1" is a mistyped IP address, not a name
    }

    /** Validates a comma-separated list; blanks are skipped, duplicates dropped. */
    @Throws(InvalidPeerException::class)
    fun parseList(list: String, defaultPort: Int = DEFAULT_PORT): List<String> {
        val out = ArrayList<String>()
        for (item in list.split(',')) {
            if (item.isBlank()) continue
            val p = normalize(item, defaultPort)
            if (p !in out) out.add(p)
        }
        if (out.size > MAX_PEERS) throw InvalidPeerException("too many addresses: at most $MAX_PEERS")
        return out
    }
}

/** Where the list is kept between app starts (app preferences on Android). */
interface PeerStore {
    fun load(): List<String>
    fun save(peers: List<String>)
}
