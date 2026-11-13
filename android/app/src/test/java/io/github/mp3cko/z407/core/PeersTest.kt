package io.github.mp3cko.z407.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/** The same cases as internal/peers/peers_test.go. */
class PeersTest {
    @Test
    fun normalizeAccepts() {
        val good = mapOf(
            "living-room-pc" to "living-room-pc:2407",
            "  Desk.Local " to "desk.local:2407",
            "pc:8080" to "pc:8080",
            "192.168.1.20" to "192.168.1.20:2407",
            "192.168.1.20:5001" to "192.168.1.20:5001",
            "localhost" to "localhost:2407",
            "my_pc" to "my_pc:2407",
            "[fe80::1]" to "[fe80::1]:2407",
            "[fe80::1]:6000" to "[fe80::1]:6000",
            "fe80::1" to "[fe80::1]:2407",
            "pc1.home.example.de" to "pc1.home.example.de:2407",
        )
        for ((input, want) in good) assertEquals(input, want, Peers.normalize(input))
    }

    @Test
    fun normalizeRejects() {
        val bad = listOf(
            "", "   ", "http://pc", "https://pc:5000", "pc/remote", "pc:5000/", "user@pc", "two words",
            "pc:", "pc:0", "pc:65536", "pc:http", ":5000", "-pc", "pc-", "pc..local", "a\$b",
            "1.2.3", "300.1.1.1", "[192.168.1.1]:5000", "[fe80::1", "[nonsense]", "fe80::zz", "[fe80::1]:x",
            "a".repeat(64), "a.".repeat(127) + "com",
        )
        for (input in bad) {
            val e = assertThrows(input, Peers.InvalidPeerException::class.java) { Peers.normalize(input) }
            assertTrue(e.message, e.message!!.contains("not a valid address"))
            assertTrue(e.message, e.message!!.contains("192.168.1.20:2408"))
        }
    }

    @Test
    fun parseList() {
        assertEquals(listOf("pc:2407", "192.168.1.20:5001"), Peers.parseList(" pc , 192.168.1.20:5001,, PC:2407 ,"))
        for (empty in listOf("", " ", ",,")) assertEquals(emptyList<String>(), Peers.parseList(empty))
        val e = assertThrows(Peers.InvalidPeerException::class.java) { Peers.parseList("pc, bad host") }
        assertTrue(e.message, e.message!!.contains("\"bad host\""))
        val many = (0..Peers.MAX_PEERS).joinToString(",") { "pc" + ('a' + it) }
        assertThrows(Peers.InvalidPeerException::class.java) { Peers.parseList(many) }
    }
}
