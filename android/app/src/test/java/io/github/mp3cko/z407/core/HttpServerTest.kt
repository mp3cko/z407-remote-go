package io.github.mp3cko.z407.core

import java.net.HttpURLConnection
import java.net.Socket
import java.net.URL
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** The phone's own server, asked the way a browser and the desktop app ask. */
class HttpServerTest {
    private val store = MemoryPeerStore()
    private val running = Running(FakeDialer()).also { it.waitConnected() }
    private val log = ArrayList<String>()
    private var disconnects = 0
    private val files = mapOf("" to "<html>remote</html>", "sw.js" to "// worker", "icon-192.png" to "png")
    private val server = HttpServer(
        Api(running.manager, store, addrs = { listOf("http://192.168.1.30:5000") }, disconnect = { synchronized(log) { disconnects++ } }),
        file = { files[it]?.toByteArray() },
        log = { synchronized(log) { log.add(it) } },
    )
    private val port = server.start(0)

    @After
    fun stop() {
        server.stop()
        running.stop()
    }

    private class Answer(val status: Int, val type: String?, val body: String)

    private fun ask(method: String, path: String, body: String? = null): Answer {
        val conn = URL("http://127.0.0.1:$port$path").openConnection() as HttpURLConnection
        try {
            conn.requestMethod = method
            conn.connectTimeout = 2000
            conn.readTimeout = 2000
            if (body != null) {
                conn.doOutput = true
                conn.outputStream.use { it.write(body.toByteArray()) }
            }
            val status = conn.responseCode
            val text = (if (status < 400) conn.inputStream else conn.errorStream)?.use { it.readBytes().toString(Charsets.UTF_8) } ?: ""
            return Answer(status, conn.contentType, text)
        } finally {
            conn.disconnect()
        }
    }

    @Test
    fun servesThePageAndItsFiles() {
        val page = ask("GET", "/")
        assertEquals(200, page.status)
        assertEquals("text/html; charset=utf-8", page.type)
        assertEquals("<html>remote</html>", page.body)
        assertEquals("text/javascript; charset=utf-8", ask("GET", "/sw.js").type)
        assertEquals("image/png", ask("GET", "/icon-192.png").type)
        assertEquals(404, ask("GET", "/nothing.txt").status)
        assertEquals(404, ask("GET", "/a/b").status)
        assertEquals(405, ask("POST", "/").status)
    }

    @Test
    fun answersTheApi() {
        val status = ask("GET", "/api/status")
        assertEquals(200, status.status)
        assertEquals("application/json", status.type)
        assertEquals("{\"connected\":true,\"note\":\"\",\"phase\":\"connected\",\"problem\":\"\",\"volume\":50}\n", status.body)
        assertEquals("{\"success\":true}\n", ask("POST", "/api/vol_up").body)
        assertEquals("{\"urls\":[\"http://192.168.1.30:5000\"]}\n", ask("GET", "/api/addrs").body)
        assertEquals(404, ask("GET", "/api/events").status)
    }

    @Test
    fun queryParametersAreDecoded() {
        val saved = ask("POST", "/api/peers?list=desk%2C%20192.168.1.20%3A5001")
        assertEquals("{\"peers\":[\"desk:2407\",\"192.168.1.20:5001\"],\"success\":true}\n", saved.body)
        assertEquals(listOf("desk:2407", "192.168.1.20:5001"), store.load())
    }

    @Test
    fun disconnectAsTheDesktopAppSendsIt() {
        // An empty body, like Go's http client and the phone app send it...
        val r = ask("POST", "/api/disconnect", "")
        assertEquals(200, r.status)
        assertEquals("{\"success\":true}\n", r.body)
        // ...and one with a body, which is read and ignored.
        assertEquals(200, ask("POST", "/api/disconnect", "please").status)
        assertEquals(2, synchronized(log) { disconnects })
    }

    @Test
    fun logsRequestsButNotThePolls() {
        ask("GET", "/api/status")
        ask("GET", "/api/log")
        ask("POST", "/api/vol_up")
        eventually("the request in the log") { synchronized(log) { log.any { it.startsWith("POST /api/vol_up -> 200 from 127.0.0.1") } } }
        assertTrue(synchronized(log) { log.none { it.contains("/api/status") || it.contains("/api/log") } })
    }

    @Test
    fun garbageIsRefusedAndTheServerLives() {
        Socket("127.0.0.1", port).use { s ->
            s.soTimeout = 2000
            s.getOutputStream().write("nonsense\r\n\r\n".toByteArray())
            val answer = s.getInputStream().readBytes().toString(Charsets.ISO_8859_1)
            assertTrue(answer, answer.startsWith("HTTP/1.1 400 "))
        }
        Socket("127.0.0.1", port).close() // a caller that says nothing
        assertEquals(200, ask("GET", "/").status)
    }

    @Test
    fun stopFreesThePort() {
        server.stop()
        val again = HttpServer(Api(running.manager, store), file = { null })
        try {
            assertEquals(port, again.start(port))
        } finally {
            again.stop()
        }
    }
}
