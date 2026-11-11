package io.github.mp3cko.z407

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build

/** The runtime permissions Bluetooth LE needs on this version of Android. */
object Permissions {
    /** Android 12+ has dedicated Bluetooth permissions; before that, scans need Location. */
    val usesLocation = Build.VERSION.SDK_INT < Build.VERSION_CODES.S

    val needed: Array<String> =
        if (usesLocation) {
            arrayOf(Manifest.permission.ACCESS_FINE_LOCATION)
        } else {
            arrayOf(Manifest.permission.BLUETOOTH_SCAN, Manifest.permission.BLUETOOTH_CONNECT)
        }

    fun granted(context: Context): Boolean =
        needed.all { context.checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }
}
