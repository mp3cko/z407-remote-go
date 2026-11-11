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

    private fun connectedApi(): Api {
        val r = Running(dialer).also { running = it }
        r.waitConnected()
        return Api(r.manager, store)
    }

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
        check(disconnectedApi().handle("GET", "/api/status", null), 200, "{\"connected\":false,\"volume\":50}\n")
        val api = connectedApi()
        api.handle("POST", "/api/vol_up", null)
        check(api.handle("GET", "/api/status", null), 200, "{\"connected\":true,\"volume\":55}\n")
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
    fun shutdownIsNotAvailableOnThePhone() {
        check(connectedApi().handle("POST", "/api/shutdown", null), 501, "{\"error\":\"Shutdown is not available\",\"success\":false}\n")
    }

    @Test
    fun thePhoneHasNoWebAddress() {
        check(connectedApi().handle("GET", "/api/addrs", null), 200, "{\"urls\":[]}\n")
        assertEquals(405, connectedApi().handle("POST", "/api/addrs", null).status)
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
