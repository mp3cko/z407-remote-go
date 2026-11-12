package io.github.mp3cko.z407

import android.content.Context
import android.os.SystemClock
import android.util.Log
import io.github.mp3cko.z407.core.Api
import io.github.mp3cko.z407.core.LogHistory
import io.github.mp3cko.z407.core.Notice
import io.github.mp3cko.z407.core.PeerStore
import io.github.mp3cko.z407.core.SpeakerManager
import io.github.mp3cko.z407.core.Takeover
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL

/**
 * The app's one connection to the speakers and everything that answers the
 * page. It lives as long as the process, so the volume estimate survives
 * screen rotations and visits to other apps.
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
        post = ::postShutdown,
        log = ::log,
        found = manager::found,
        note = notice::set,
    )
    val api = Api(manager, store, notice::get, history)

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

    /** Connects and stays connected. Called while the app is on screen. */
    @Synchronized
    fun start() {
        if (threads.isNotEmpty()) return
        manager.resume()
        takeover.newSession()
        threads = listOf(
            Thread({ manager.run() }, "z407-speaker"),
            Thread({
                try {
                    while (true) {
                        takeover.check(SystemClock.elapsedRealtime())
                        Thread.sleep(Takeover.CHECK_INTERVAL_MILLIS)
                    }
                } catch (_: InterruptedException) {
                    // stop()
                }
            }, "z407-takeover"),
        ).onEach { it.start() }
    }

    /**
     * Releases the speakers, so that other controllers can connect while the
     * app is in the background (the speakers accept only one).
     */
    @Synchronized
    fun stop() {
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

    /** Sends POST /api/shutdown to a desktop instance; returns e.g. `200 {"success":true}`. */
    private fun postShutdown(peer: String): String {
        val conn = URL("http://$peer/api/shutdown").openConnection() as HttpURLConnection
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
