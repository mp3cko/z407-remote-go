package io.github.mp3cko.z407

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.drawable.Icon
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.util.Log
import io.github.mp3cko.z407.core.Summary

/**
 * Keeps the remote working while the app is not on screen: the connection to
 * the speakers, and the server other devices reach the remote through. A
 * foreground service, so Android lets it live; its notification says how
 * things stand, carries the keys used most, and opens the app when tapped.
 *
 * It runs from the first time the app is opened until the Quit key of the
 * notification is pressed or the app is removed from the list of recent apps.
 */
class RemoteService : Service() {
    companion object {
        private const val TAG = "Z407"
        private const val CHANNEL = "remote"
        private const val NOTIFICATION = 1
        private const val COMMAND = "io.github.mp3cko.z407.COMMAND."
        private const val QUIT = "io.github.mp3cko.z407.QUIT"
        private const val REFRESH_MILLIS = 2_000L

        /** Call it while the app is on screen: only then may a foreground service start. */
        fun start(context: Context) {
            try {
                context.startForegroundService(Intent(context, RemoteService::class.java))
            } catch (e: RuntimeException) { // refused by the system; the app still works while on screen
                Log.w(TAG, "Could not start the background service", e)
            }
        }
    }

    private lateinit var remote: Remote
    private val handler = Handler(Looper.getMainLooper())
    private var shown: Summary? = null
    private val refresh = object : Runnable {
        override fun run() {
            val now = remote.summary()
            if (now != shown) {
                shown = now
                getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification(now))
            }
            handler.postDelayed(this, REFRESH_MILLIS)
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        remote = Remote.get(this)
        val channel = NotificationChannel(CHANNEL, getString(R.string.channel_name), NotificationManager.IMPORTANCE_LOW)
        channel.description = getString(R.string.channel_description)
        channel.setShowBadge(false)
        getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == QUIT) {
            remote.quit() // closes the app's screen too, if it is open
            stopSelf()
            return START_NOT_STICKY
        }
        val summary = remote.summary()
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                startForeground(NOTIFICATION, notification(summary), ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE)
            } else {
                startForeground(NOTIFICATION, notification(summary))
            }
        } catch (e: RuntimeException) {
            // Android refuses when the Bluetooth permission is gone, or when
            // it restarts the service at a moment it does not allow one.
            Log.w(TAG, "Could not run in the foreground", e)
            stopSelf()
            return START_NOT_STICKY
        }
        if (!Permissions.granted(this)) { // withdrawn in the settings meanwhile
            stopSelf()
            return START_NOT_STICKY
        }
        shown = summary
        remote.start()
        intent?.action?.takeIf { it.startsWith(COMMAND) }?.let { remote.press(it.removePrefix(COMMAND)) }
        handler.removeCallbacks(refresh)
        // Soon, to show what a key just pressed did; then at leisure.
        handler.postDelayed(refresh, if (intent?.action == null) REFRESH_MILLIS else 400)
        return START_STICKY
    }

    /** The app was swiped away from the recent apps: the user is done with it. */
    override fun onTaskRemoved(rootIntent: Intent?) {
        stopSelf()
    }

    override fun onDestroy() {
        handler.removeCallbacks(refresh)
        remote.stop()
        super.onDestroy()
    }

    private fun notification(summary: Summary): Notification {
        val open = PendingIntent.getActivity(
            this, 0,
            packageManager.getLaunchIntentForPackage(packageName) ?: Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val builder = Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_remote)
            .setColor(getColor(R.color.icon_tile))
            .setContentTitle(getString(R.string.notification_title, summary.title))
            .setContentText(summary.text)
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setShowWhen(false)
            .setCategory(Notification.CATEGORY_SERVICE)
            .setVisibility(Notification.VISIBILITY_PUBLIC)
            .addAction(key(R.drawable.ic_volume_down, R.string.key_volume_down, "vol_down"))
            .addAction(key(R.drawable.ic_play_pause, R.string.key_play_pause, "play_pause"))
            .addAction(key(R.drawable.ic_volume_up, R.string.key_volume_up, "vol_up"))
            .addAction(action(R.drawable.ic_quit, R.string.key_quit, QUIT))
            // Four keys are one more than a plain notification shows. This
            // style shows them all when expanded, and the first three as
            // icons when not.
            .setStyle(Notification.MediaStyle().setShowActionsInCompactView(0, 1, 2))
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            builder.setForegroundServiceBehavior(Notification.FOREGROUND_SERVICE_IMMEDIATE) // no ten-second wait
        }
        return builder.build()
    }

    /** A key of the notification that sends [command] ("vol_up") to the speakers. */
    private fun key(icon: Int, label: Int, command: String) = action(icon, label, COMMAND + command)

    /** A key of the notification: it sends [action] to this service. */
    private fun action(icon: Int, label: Int, action: String): Notification.Action {
        val press = PendingIntent.getService(
            this, 0, Intent(this, RemoteService::class.java).setAction(action), PendingIntent.FLAG_IMMUTABLE,
        )
        return Notification.Action.Builder(Icon.createWithResource(this, icon), getString(label), press).build()
    }
}
