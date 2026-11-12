package io.github.mp3cko.z407.core

import java.io.IOException

/** An HTTP answer for the page. */
data class ApiResponse(val status: Int, val reason: String, val body: String) {
    val contentType get() = if (body.startsWith("{")) "application/json" else "text/plain; charset=utf-8"
}

/**
 * Answers the page's /api requests exactly like internal/server/server.go,
 * so the page needs no Android-specific code. It answers the page inside the
 * app and, through [HttpServer], callers on the network alike. Only method,
 * path and query string are used: a WebView cannot read the body of a
 * request it intercepts.
 */
class Api(
    private val speaker: SpeakerManager,
    private val store: PeerStore,
    /** The passing message the display shows. */
    private val notice: () -> String = { "" },
    /** What the log viewer shows; null = nothing. */
    private val history: LogHistory? = null,
    /** Where other devices can open the remote ("http://192.168.1.30:5000"); none while no server runs. */
    private val addrs: () -> List<String> = { emptyList() },
    /** Another device asks for the speakers (POST /api/disconnect): lets go of them until [reconnect]. */
    private val disconnect: () -> Unit = {},
    /** The user takes the speakers back (POST /api/reconnect). */
    private val reconnect: () -> Unit = {},
    /** Whether the speakers were let go of and not taken back yet. */
    private val released: () -> Boolean = { false },
) {
    companion object {
        const val NOT_CONNECTED = "Speakers not connected. Waiting for background connection..."
    }

    private val commands: Map<String, () -> Unit> = mapOf(
        "vol_up" to speaker::volumeUp,
        "vol_down" to speaker::volumeDown,
        "play_pause" to speaker::playPause,
        "input_aux" to speaker::inputAux,
        "input_bluetooth" to speaker::inputBluetooth,
        "bluetooth_pair" to speaker::bluetoothPair,
        "factory_reset" to speaker::factoryReset,
        "input_usb" to speaker::inputUsb,
        "bass_up" to speaker::bassUp,
        "bass_down" to speaker::bassDown,
        "next_speaker" to speaker::nextTrack,
        "prev_speaker" to speaker::prevTrack,
    )

    /**
     * [path] is the URL path ("/api/vol_up"); [list] and [after] are the
     * decoded ?list= and ?after= parameters, if any.
     */
    fun handle(method: String, path: String, list: String?, after: String? = null): ApiResponse {
        val name = path.removePrefix("/api/")
        if (!path.startsWith("/api/") || name.isEmpty() || name.contains('/')) return notFound()
        return when (name) {
            "status" -> if (method == "GET") status() else methodNotAllowed()
            "peers" -> when (method) {
                "GET" -> json(200, "fixed" to false, "peers" to store.load())
                "POST" -> savePeers(list ?: "")
                else -> methodNotAllowed()
            }
            "log" -> if (method == "GET") logLines(after) else methodNotAllowed()
            "addrs" -> if (method == "GET") json(200, "urls" to addrs()) else methodNotAllowed()
            // The page's event stream has no meaning here.
            "events" -> if (method == "GET") notFound() else methodNotAllowed()
            // Another device asks for the speakers; the page then offers to reconnect.
            "disconnect", "reconnect" -> if (method == "POST") {
                if (name == "disconnect") disconnect() else reconnect()
                json(200, "success" to true)
            } else methodNotAllowed()
            else -> if (method == "POST") command(name) else methodNotAllowed()
        }
    }

    private fun status(): ApiResponse {
        val s = speaker.status()
        if (released()) {
            return json(200, "connected" to false, "note" to notice(), "phase" to "released", "problem" to "", "volume" to s.volume)
        }
        val phase = when {
            s.connected -> "connected"
            s.phase.isEmpty() -> "waiting" // for the next attempt
            else -> s.phase
        }
        return json(
            200, "connected" to s.connected, "note" to notice(), "phase" to phase, "problem" to s.problem,
            "volume" to s.volume,
        )
    }

    /** The remembered log lines after line number [after] (all of them without it). */
    private fun logLines(after: String?): ApiResponse {
        val (entries, last) = history?.since(after?.toIntOrNull() ?: 0) ?: (emptyList<LogEntry>() to 0)
        val lines = entries.map { mapOf("level" to it.level, "n" to it.n, "text" to it.text, "time" to it.time) }
        return json(200, "lines" to lines, "next" to last)
    }

    private fun command(name: String): ApiResponse {
        if (released() || !speaker.status().connected) return json(404, "error" to NOT_CONNECTED, "success" to false)
        val fn = commands[name] ?: return json(400, "error" to "Unknown command", "success" to false)
        return try {
            fn()
            json(200, "success" to true)
        } catch (e: IOException) {
            json(500, "error" to (e.message ?: "error"), "success" to false)
        }
    }

    private fun savePeers(list: String): ApiResponse = try {
        val peers = Peers.parseList(list)
        store.save(peers)
        json(200, "peers" to peers, "success" to true)
    } catch (e: Peers.InvalidPeerException) {
        json(400, "error" to (e.message ?: "invalid"), "success" to false)
    }

    private fun notFound() = ApiResponse(404, "Not Found", "404 page not found\n")
    private fun methodNotAllowed() = ApiResponse(405, "Method Not Allowed", "Method Not Allowed\n")

    /** Encodes like Go's encoding/json does for a map: keys sorted, newline at the end. */
    private fun json(status: Int, vararg fields: Pair<String, Any>): ApiResponse {
        val body = fields.sortedBy { it.first }.joinToString(",", "{", "}\n") { (k, v) -> quote(k) + ":" + encode(v) }
        return ApiResponse(status, reasons[status] ?: "Error", body)
    }

    private val reasons = mapOf(
        200 to "OK", 400 to "Bad Request", 404 to "Not Found", 500 to "Internal Server Error", 501 to "Not Implemented",
    )

    private fun encode(v: Any): String = when (v) {
        is Boolean, is Int -> v.toString()
        is List<*> -> v.joinToString(",", "[", "]") { encode(it!!) }
        is Map<*, *> -> v.entries.sortedBy { it.key as String }
            .joinToString(",", "{", "}") { quote(it.key as String) + ":" + encode(it.value!!) }
        else -> quote(v.toString())
    }

    private fun quote(s: String): String {
        val sb = StringBuilder("\"")
        for (c in s) {
            when {
                c == '"' -> sb.append("\\\"")
                c == '\\' -> sb.append("\\\\")
                c == '\n' -> sb.append("\\n")
                c == '\r' -> sb.append("\\r")
                c == '\t' -> sb.append("\\t")
                // Go escapes these too, to keep JSON safe inside HTML.
                c < ' ' || c == '<' || c == '>' || c == '&' || c == '\u2028' || c == '\u2029' ->
                    sb.append("\\u%04x".format(c.code))
                else -> sb.append(c)
            }
        }
        return sb.append('"').toString()
    }
}
