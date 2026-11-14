package io.github.mp3cko.z407

import android.content.Context
import android.os.SystemClock
import android.util.Log
import io.github.mp3cko.z407.core.Addrs
import io.github.mp3cko.z407.core.Api
import io.github.mp3cko.z407.core.HttpServer
import io.github.mp3cko.z407.core.LogHistory
import io.github.mp3cko.z407.core.Notice
import io.github.mp3cko.z407.core.PeerStore
import io.github.mp3cko.z407.core.Peers
import io.github.mp3cko.z407.core.SpeakerManager
import io.github.mp3cko.z407.core.Summary
import io.github.mp3cko.z407.core.Takeover
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL

/**
 * The app's one connection to the speakers and everything that answers the
 * page. It lives as long as the process, so the volume estimate survives
 * screen rotations and visits to other apps. [RemoteService] keeps it
 * working while the app is not on screen.
 */
class Remote private constructor(context: Context) {
    companion object {
        private const val TAG = "Z407"

        @Volatile private var instance: Remote? = null

        fun get(context: Context): Remote =
            instance ?: synchronized(this) {
                instance ?: Remote(context.applicationContext).also { instance = it }
            }
    }

    private val store = PrefsPeerStore(context)
    private val history = LogHistory()
    private val notice = Notice()
    private val manager = SpeakerManager(BleDialer(context), log = ::log)
    private val takeover = Takeover(
        peers = store::load,
        connected = { manager.status().connected },
        post = ::postDisconnect,
        log = ::log,
        found = manager::found,
        note = notice::set,
    )
    private val assets = context.assets

    /** Answers the page, inside the app and for callers on the network alike. */
    val api = Api(
        manager, store, notice::get, history, ::addrs,
        // Closing the link takes a moment; the caller does not wait for it.
        disconnect = { Thread(::release, "z407-release").start() },
        reconnect = ::reconnect,
        released = { released },
        again = ::opened,
        log = ::log,
    )
    private val server = HttpServer(api, ::webFile, ::log)

    @Volatile private var serving = false
    /** Another device asked for the speakers and has them until the user reconnects. */
    @Volatile private var released = false

    private fun addrs(): List<String> =
        if (serving) Addrs.lan().map { "http://$it:${Peers.DEFAULT_PORT}" } else emptyList()

    /** The page ("") and the files next to it, for browsers on the network. */
    private fun webFile(name: String): ByteArray? = try {
        assets.open(if (name.isEmpty()) "index.html" else "static/$name").use { it.readBytes() }
    } catch (_: IOException) {
        null
    }

    /** Logs to logcat and to the log viewer of the remote. */
    private fun log(line: String) {
        Log.i(TAG, line)
        val level = when {
            line.startsWith("Connected") -> LogHistory.OK
            line.contains("failed") -> LogHistory.ERROR
            else -> LogHistory.INFO
        }
        history.add(level, line)
    }

    private var threads: List<Thread> = emptyList()
    private var active = false
    /** The app came to the screen: the takeover thread starts a new session. */
    @Volatile private var opened = false

    /** What the notification shows. */
    fun summary(): Summary = Summary.of(manager.status(), released)

    /** A key of the notification was pressed: [command] is the name the page uses ("vol_up"). */
    fun press(command: String) {
        Thread({ api.handle("POST", "/api/$command", null) }, "z407-key").start()
    }

    /** Closes the app's screen; set by the activity while there is one. */
    @Volatile var onQuit: (() -> Unit)? = null

    /** The Quit key of the notification: the screen goes too, the service stops the rest. */
    fun quit() {
        onQuit?.invoke()
    }

    /**
     * The app came to the screen, or the user asked for it on the page. If
     * it is still looking for the speakers, the other devices are asked for
     * them once more.
     */
    fun opened() {
        opened = true
    }

    /**
     * Connects and stays connected, and serves the remote to the network,
     * until [stop].
     */
    @Synchronized
    fun start() {
        if (!serving) {
            try {
                server.start(Peers.DEFAULT_PORT)
                serving = true
                log("Serving the remote on port ${Peers.DEFAULT_PORT}")
            } catch (e: IOException) {
                log("Port ${Peers.DEFAULT_PORT} is taken (${e.message}): other devices cannot reach this app")
            }
        }
        active = true
        if (!released) connect()
    }

    private fun connect() {
        if (threads.isNotEmpty()) return
        manager.resume()
        opened = true
        threads = listOf(
            Thread({ manager.run() }, "z407-speaker"),
            Thread({
                try {
                    while (true) {
                        if (opened) {
                            opened = false
                            takeover.newSession()
                        }
                        takeover.check(SystemClock.elapsedRealtime())
                        Thread.sleep(Takeover.CHECK_INTERVAL_MILLIS)
                    }
                } catch (_: InterruptedException) {
                    // disconnect()
                }
            }, "z407-takeover"),
        ).onEach { it.start() }
    }

    /**
     * Releases the speakers and stops serving: the app was removed from the
     * recent apps. Opened again it connects, also after another device had
     * asked for the speakers.
     */
    @Synchronized
    fun stop() {
        server.stop()
        serving = false
        active = false
        released = false
        disconnect()
    }

    /** POST /api/disconnect: lets go of the speakers until the user reconnects. */
    @Synchronized
    private fun release() {
        if (!active || released) return // the app was closed meanwhile, or was asked twice
        released = true
        log("Another device asked for the speakers: disconnected until you reconnect")
        disconnect()
    }

    /** POST /api/reconnect: takes the speakers back, asking the other computers for them again. */
    @Synchronized
    private fun reconnect() {
        if (!released) return
        released = false
        log("Reconnecting")
        if (active) connect()
    }

    private fun disconnect() {
        manager.stop()
        threads.forEach { it.interrupt() }
        threads.forEach {
            try {
                it.join(2000)
            } catch (_: InterruptedException) {
                Thread.currentThread().interrupt()
            }
        }
        threads = emptyList()
    }

    /** Sends POST /api/disconnect to another instance; returns e.g. `200 {"success":true}`. */
    private fun postDisconnect(peer: String): String {
        val conn = URL("http://$peer/api/disconnect").openConnection() as HttpURLConnection
        try {
            conn.requestMethod = "POST"
            conn.connectTimeout = Takeover.PEER_TIMEOUT_MILLIS
            conn.readTimeout = Takeover.PEER_TIMEOUT_MILLIS
            conn.doOutput = true
            conn.setFixedLengthStreamingMode(0)
            conn.outputStream.close()
            val code = conn.responseCode
            val body = (if (code < 400) conn.inputStream else conn.errorStream)
                ?.use { it.readBytes().take(200).toByteArray().toString(Charsets.UTF_8).trim() } ?: ""
            return "$code $body"
        } catch (e: RuntimeException) { // e.g. a malformed saved address
            throw IOException(e.message, e)
        } finally {
            conn.disconnect()
        }
    }
}

/** The peer list in the app's preferences. */
class PrefsPeerStore(context: Context) : PeerStore {
    private val prefs = context.getSharedPreferences("z407", Context.MODE_PRIVATE)

    override fun load(): List<String> =
        prefs.getString("peers", "")!!.split(',').filter { it.isNotBlank() }

    override fun save(peers: List<String>) {
        prefs.edit().putString("peers", peers.joinToString(",")).apply()
    }
}
