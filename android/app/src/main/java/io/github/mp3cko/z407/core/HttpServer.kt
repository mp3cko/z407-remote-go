package io.github.mp3cko.z407.core

import java.io.BufferedInputStream
import java.io.IOException
import java.io.InputStream
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.URLDecoder
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

/**
 * Serves the remote to the network, as the desktop app does: the page, the
 * files next to it and the /api requests, which [api] answers. Other devices
 * can open the remote in a browser, and a desktop copy of the app can ask
 * this one to release the speakers (POST /api/disconnect).
 *
 * It speaks only as much HTTP as that takes: one request per connection,
 * and a request body is read and ignored (the API has everything in the
 * path and the query string). There is no event stream; the page copes.
 */
class HttpServer(
    private val api: Api,
    /** The file called [name] ("sw.js"); "" is the page. null = there is none. */
    private val file: (name: String) -> ByteArray?,
    private val log: (String) -> Unit = {},
) {
    private companion object {
        /** A caller that stops talking is dropped after this long. */
        const val TIMEOUT_MILLIS = 5_000
        const val MAX_HEAD = 8 * 1024
        const val MAX_BODY = 64 * 1024L

        val types = mapOf(
            "html" to "text/html; charset=utf-8",
            "js" to "text/javascript; charset=utf-8",
            "png" to "image/png",
            "webmanifest" to "application/manifest+json",
        )
        val reasons = mapOf(400 to "Bad Request", 404 to "Not Found", 405 to "Method Not Allowed")
    }

    private var socket: ServerSocket? = null
    private var workers: ExecutorService? = null

    /** Listens on [port] (0 = any free one) and returns the port in use. */
    @Synchronized
    @Throws(IOException::class)
    fun start(port: Int): Int {
        socket?.let { return it.localPort }
        val s = ServerSocket()
        try {
            s.reuseAddress = true
            s.bind(InetSocketAddress(port))
        } catch (e: IOException) {
            s.close()
            throw e
        }
        val pool = Executors.newCachedThreadPool { r -> Thread(r, "z407-http").apply { isDaemon = true } }
        socket = s
        workers = pool
        pool.execute {
            while (true) {
                val client = try {
                    s.accept()
                } catch (_: IOException) {
                    break // stop()
                }
                try {
                    pool.execute { serve(client) }
                } catch (_: RuntimeException) { // stop() came in between
                    client.close()
                }
            }
        }
        return s.localPort
    }

    /** Stops listening. Requests under way end by themselves. */
    @Synchronized
    fun stop() {
        try {
            socket?.close()
        } catch (_: IOException) {
            // closed is closed
        }
        workers?.shutdown()
        socket = null
        workers = null
    }

    private class Answer(val status: Int, val reason: String, val type: String, val body: ByteArray, val cache: String = "")

    private fun plain(status: Int) =
        Answer(status, reasons.getValue(status), "text/plain; charset=utf-8", "${reasons[status]}\n".toByteArray())

    private fun serve(client: Socket) {
        try {
            client.use {
                it.soTimeout = TIMEOUT_MILLIS
                val input = BufferedInputStream(it.getInputStream())
                val head = readHead(input) ?: return // the caller said nothing
                val request = head.first().split(' ')
                val answer = if (request.size != 3 || !request[2].startsWith("HTTP/1.") || !request[1].startsWith("/")) {
                    plain(400)
                } else {
                    skipBody(input, head)
                    answer(request[0], request[1]).also { a -> logRequest(request[0], request[1], a.status, it) }
                }
                val out = it.getOutputStream()
                out.write(
                    ("HTTP/1.1 ${answer.status} ${answer.reason}\r\n" +
                        "Content-Type: ${answer.type}\r\n" +
                        "Content-Length: ${answer.body.size}\r\n" +
                        (if (answer.cache.isEmpty()) "" else "Cache-Control: ${answer.cache}\r\n") +
                        "Connection: close\r\n\r\n").toByteArray(Charsets.ISO_8859_1)
                )
                out.write(answer.body)
                out.flush()
            }
        } catch (_: IOException) {
            // the caller went away
        }
    }

    /** The request line and the header lines; null when the caller sent nothing whole. */
    private fun readHead(input: InputStream): List<String>? {
        val lines = ArrayList<String>()
        val line = StringBuilder()
        var size = 0
        while (size++ < MAX_HEAD) {
            when (val c = input.read()) {
                -1 -> return null
                '\r'.code -> {}
                '\n'.code -> {
                    if (line.isEmpty()) return lines.ifEmpty { null }
                    lines.add(line.toString())
                    line.setLength(0)
                }
                else -> line.append(c.toChar())
            }
        }
        return null
    }

    private fun skipBody(input: InputStream, head: List<String>) {
        val length = head.drop(1).firstOrNull { it.startsWith("content-length:", ignoreCase = true) }
            ?.substringAfter(':')?.trim()?.toLongOrNull() ?: return
        var left = minOf(length, MAX_BODY)
        while (left > 0) {
            val n = input.skip(left)
            if (n <= 0 && input.read() < 0) return
            left -= maxOf(n, 1)
        }
    }

    private fun answer(method: String, target: String): Answer {
        val path = target.substringBefore('?')
        if (path.startsWith("/api/")) {
            val query = target.substringAfter('?', "").split('&').filter { it.isNotEmpty() }.associate {
                decode(it.substringBefore('=')) to decode(it.substringAfter('=', ""))
            }
            val r = api.handle(method, path, query["list"], query["after"])
            return Answer(r.status, r.reason, r.contentType, r.body.toByteArray(), "no-store")
        }
        val name = path.removePrefix("/")
        if (name.contains('/')) return plain(404)
        val data = file(name) ?: return plain(404)
        if (method != "GET") return plain(405)
        val type = if (name.isEmpty()) types.getValue("html") else types[name.substringAfterLast('.', "")] ?: "application/octet-stream"
        // Browsers must notice a new service worker.
        return Answer(200, "OK", type, data, if (name.endsWith(".js")) "no-cache" else "")
    }

    private fun decode(s: String): String = try {
        URLDecoder.decode(s, "UTF-8")
    } catch (_: IllegalArgumentException) { // a stray %
        s
    }

    /** Like the desktop app: every request except the page's polls. */
    private fun logRequest(method: String, target: String, status: Int, client: Socket) {
        val path = target.substringBefore('?')
        if (method == "GET" && (path == "/api/status" || path == "/api/log" || path == "/api/events")) return
        log("$method $path -> $status from ${client.inetAddress?.hostAddress}")
    }
}
