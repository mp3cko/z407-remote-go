package io.github.mp3cko.z407.core

import java.io.IOException

/** An HTTP answer for the page. */
data class ApiResponse(val status: Int, val reason: String, val body: String) {
    val contentType get() = if (body.startsWith("{")) "application/json" else "text/plain; charset=utf-8"
}

/**
 * Answers the page's /api requests exactly like internal/server/server.go,
 * so the page needs no Android-specific code. Only method, path and query
 * string are used: a WebView cannot read the body of a request it intercepts.
 */
class Api(private val speaker: SpeakerManager, private val store: PeerStore) {
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

    /** [path] is the URL path ("/api/vol_up"); [list] the decoded ?list= parameter, if any. */
    fun handle(method: String, path: String, list: String?): ApiResponse {
        val name = path.removePrefix("/api/")
        if (!path.startsWith("/api/") || name.isEmpty() || name.contains('/')) return notFound()
        return when (name) {
            "status" -> if (method == "GET") status() else methodNotAllowed()
            "peers" -> when (method) {
                "GET" -> json(200, "fixed" to false, "peers" to store.load())
                "POST" -> savePeers(list ?: "")
                else -> methodNotAllowed()
            }
            // The phone runs no server: there is no address to open the remote at...
            "addrs" -> if (method == "GET") json(200, "urls" to emptyList<String>()) else methodNotAllowed()
            // ...the page's event stream has no meaning here...
            "events" -> if (method == "GET") notFound() else methodNotAllowed()
            // ...and nothing can shut the phone down remotely.
            "shutdown" -> if (method == "POST") {
                json(501, "error" to "Shutdown is not available", "success" to false)
            } else methodNotAllowed()
            else -> if (method == "POST") command(name) else methodNotAllowed()
        }
    }

    private fun status(): ApiResponse {
        val s = speaker.status()
        return json(200, "connected" to s.connected, "volume" to s.volume)
    }

    private fun command(name: String): ApiResponse {
        if (!speaker.status().connected) return json(404, "error" to NOT_CONNECTED, "success" to false)
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
        is List<*> -> v.joinToString(",", "[", "]") { quote(it as String) }
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
