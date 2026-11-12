package io.github.mp3cko.z407.core

import java.io.IOException
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The bodies are the literal output of the Go server; the same strings are
 * asserted in internal/server/android_parity_test.go, so the two cannot
 * drift apart unnoticed.
 */
class ApiTest {
    private val dialer = FakeDialer()
    private val store = MemoryPeerStore()
    private var running: Running? = null

    private fun running(): Running = Running(dialer).also {
        running = it
        it.waitConnected()
    }

    private fun connectedApi() = Api(running().manager, store)

    private fun disconnectedApi() = Api(SpeakerManager(FakeDialer()), store)

    @After
    fun stop() {
        running?.stop()
    }

    private fun check(r: ApiResponse, status: Int, body: String) {
        assertEquals(body, r.body)
        assertEquals(status, r.status)
        assertEquals("application/json", r.contentType)
    }

    @Test
    fun status() {
        check(disconnectedApi().handle("GET", "/api/status", null), 200, "{\"connected\":false,\"note\":\"\",\"phase\":\"waiting\",\"problem\":\"\",\"volume\":50}\n")
        val api = connectedApi()
        api.handle("POST", "/api/vol_up", null)
        check(api.handle("GET", "/api/status", null), 200, "{\"connected\":true,\"note\":\"\",\"phase\":\"connected\",\"problem\":\"\",\"volume\":55}\n")
        check(api.handle("GET", "/api/log", null), 200, "{\"lines\":[],\"next\":0}\n")
        assertEquals(405, api.handle("POST", "/api/log", null).status)
    }

    @Test
    fun logViewerAndNotice() {
        @Suppress("DEPRECATION")
        val history = LogHistory(now = { java.util.Date(126, 0, 2, 21, 4, 5) })
        history.add(LogHistory.INFO, "Searching")
        history.add(LogHistory.ERROR, "Dial failed: \"x\" <y>")
        history.add(LogHistory.OK, "Connected to Z407")
        val notice = Notice()
        notice.set("Asking desk:5000 to release the speakers")
        val api = Api(SpeakerManager(FakeDialer()), store, notice::get, history)

        val one = "{\"level\":\"info\",\"n\":1,\"text\":\"Searching\",\"time\":\"21:04:05\"}"
        val two = "{\"level\":\"error\",\"n\":2,\"text\":\"Dial failed: \\\"x\\\" \\u003cy\\u003e\",\"time\":\"21:04:05\"}"
        val three = "{\"level\":\"ok\",\"n\":3,\"text\":\"Connected to Z407\",\"time\":\"21:04:05\"}"
        val all = "{\"lines\":[$one,$two,$three],\"next\":3}\n"
        check(api.handle("GET", "/api/log", null), 200, all)
        check(api.handle("GET", "/api/log", null, "2"), 200, "{\"lines\":[$three],\"next\":3}\n")
        check(api.handle("GET", "/api/log", null, "3"), 200, "{\"lines\":[],\"next\":3}\n")
        check(api.handle("GET", "/api/log", null, "junk"), 200, all)
        check(
            api.handle("GET", "/api/status", null), 200,
            "{\"connected\":false,\"note\":\"Asking desk:5000 to release the speakers\",\"phase\":\"waiting\",\"problem\":\"\",\"volume\":50}\n",
        )
    }

    @Test
    fun logHistoryDropsTheOldestLines() {
        val history = LogHistory(max = 2)
        for (i in 1..3) history.add(LogHistory.INFO, "line $i")
        val (lines, last) = history.since(0)
        assertEquals(listOf(2, 3), lines.map { it.n })
        assertEquals(3, last)
    }

    @Test
    fun noticeGoesAwayByItself() {
        var now = 0L
        val notice = Notice { now }
        assertEquals("", notice.get())
        notice.set("one")
        now = Notice.TIME_MILLIS - 1
        assertEquals("one", notice.get())
        now = Notice.TIME_MILLIS
        assertEquals("", notice.get())
    }

    @Test
    fun everyCommandReachesTheSpeaker() {
        val api = connectedApi()
        val cases = listOf(
            "vol_up" to "8002", "vol_down" to "8003", "play_pause" to "8004", "input_aux" to "8102",
            "input_bluetooth" to "8101", "bluetooth_pair" to "8200", "factory_reset" to "8300",
            "input_usb" to "8103", "bass_up" to "8000", "bass_down" to "8001",
            "next_speaker" to "8005", "prev_speaker" to "8006",
        )
        for ((cmd, _) in cases) check(api.handle("POST", "/api/$cmd", null), 200, "{\"success\":true}\n")
        assertEquals(listOf("8405") + cases.map { it.second }, dialer.link(0)!!.written())
    }

    @Test
    fun notConnected() {
        val api = disconnectedApi()
        val body = "{\"error\":\"Speakers not connected. Waiting for background connection...\",\"success\":false}\n"
        check(api.handle("POST", "/api/vol_up", null), 404, body)
        check(api.handle("POST", "/api/nonsense", null), 404, body)
    }

    @Test
    fun unknownCommand() {
        check(connectedApi().handle("POST", "/api/bogus", null), 400, "{\"error\":\"Unknown command\",\"success\":false}\n")
    }

    @Test
    fun speakerError() {
        val api = connectedApi()
        dialer.link(0)!!.writeError = IOException("boom")
        check(api.handle("POST", "/api/vol_up", null), 500, "{\"error\":\"write 8002: boom\",\"success\":false}\n")
    }

    @Test
    fun peersReadAndSave() {
        val api = disconnectedApi()
        check(api.handle("GET", "/api/peers", null), 200, "{\"fixed\":false,\"peers\":[]}\n")
        check(
            api.handle("POST", "/api/peers", "desk, 192.168.1.20:5001"), 200,
            "{\"peers\":[\"desk:5000\",\"192.168.1.20:5001\"],\"success\":true}\n",
        )
        assertEquals(listOf("desk:5000", "192.168.1.20:5001"), store.load())
        check(api.handle("GET", "/api/peers", null), 200, "{\"fixed\":false,\"peers\":[\"desk:5000\",\"192.168.1.20:5001\"]}\n")
        // The list may be emptied, with or without the parameter.
        check(api.handle("POST", "/api/peers", ""), 200, "{\"peers\":[],\"success\":true}\n")
        check(api.handle("POST", "/api/peers", null), 200, "{\"peers\":[],\"success\":true}\n")
    }

    @Test
    fun peersValidation() {
        val api = disconnectedApi()
        api.handle("POST", "/api/peers", "good-pc")
        check(
            api.handle("POST", "/api/peers", "good-pc, http://bad/"), 400,
            "{\"error\":\"\\\"http://bad/\\\" is not a valid address (leave out http:// and any path). " +
                "Use a name or IP address, optionally with a port: living-room-pc or 192.168.1.20:5000\",\"success\":false}\n",
        )
        assertEquals(listOf("good-pc:5000"), store.load())
    }

    @Test
    fun jsonEscaping() {
        // Go escapes <, > and & inside JSON strings; so does the app.
        check(
            disconnectedApi().handle("POST", "/api/peers", "<a&b>"), 400,
            "{\"error\":\"\\\"\\u003ca\\u0026b\\u003e\\\" is not a valid address (the name may only contain letters, digits, " +
                "hyphens and dots). Use a name or IP address, optionally with a port: living-room-pc or 192.168.1.20:5000\",\"success\":false}\n",
        )
    }

    @Test
    fun disconnectAndReconnect() {
        var released = false
        val api = Api(
            running().manager, store,
            disconnect = { released = true }, reconnect = { released = false }, released = { released },
        )
        check(api.handle("POST", "/api/disconnect", null), 200, "{\"success\":true}\n")
        assertEquals(true, released)
        // Released: the page says so, whatever the manager is doing, and commands are refused.
        check(api.handle("GET", "/api/status", null), 200, "{\"connected\":false,\"note\":\"\",\"phase\":\"released\",\"problem\":\"\",\"volume\":50}\n")
        assertEquals(404, api.handle("POST", "/api/vol_up", null).status)
        check(api.handle("POST", "/api/reconnect", null), 200, "{\"success\":true}\n")
        assertEquals(false, released)
        assertEquals(405, api.handle("GET", "/api/disconnect", null).status)
        assertEquals(405, api.handle("GET", "/api/reconnect", null).status)
        // The defaults answer the same; they are what the desktop parity test compares with.
        check(connectedApi().handle("POST", "/api/disconnect", null), 200, "{\"success\":true}\n")
    }

    @Test
    fun webAddresses() {
        check(connectedApi().handle("GET", "/api/addrs", null), 200, "{\"urls\":[]}\n")
        assertEquals(405, connectedApi().handle("POST", "/api/addrs", null).status)
        val api = Api(SpeakerManager(FakeDialer()), store, addrs = { listOf("http://192.168.1.30:5000") })
        check(api.handle("GET", "/api/addrs", null), 200, "{\"urls\":[\"http://192.168.1.30:5000\"]}\n")
    }

    @Test
    fun otherRequests() {
        val api = connectedApi()
        assertEquals(404, api.handle("GET", "/api/events", null).status) // no event stream: the page copes
        assertEquals(405, api.handle("GET", "/api/vol_up", null).status)
        assertEquals(405, api.handle("POST", "/api/status", null).status)
        assertEquals(405, api.handle("PUT", "/api/peers", null).status)
        assertEquals(404, api.handle("GET", "/api/", null).status)
        assertEquals(404, api.handle("POST", "/api/a/b", null).status)
        assertEquals(404, api.handle("GET", "/elsewhere", null).status)
        assertEquals(listOf("8405"), dialer.link(0)!!.written())
    }
}
