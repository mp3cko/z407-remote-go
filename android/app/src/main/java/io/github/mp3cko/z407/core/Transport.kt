package io.github.mp3cko.z407.core

import java.io.IOException

/** Events a [Link] reports. They may arrive on any thread. */
interface LinkEvents {
    /** Called during the dial, once the speakers have been seen and the connection to them begins. */
    fun onFound() {}
    fun onNotify(data: ByteArray)
    fun onDisconnect()
}

/** An established connection to the speaker. */
interface Link {
    /** Sends [cmd] as a GATT write without response to the command characteristic. */
    @Throws(IOException::class)
    fun write(cmd: ByteArray)

    /** Disconnects. Does not trigger [LinkEvents.onDisconnect]. */
    fun close()
}

/**
 * The Bluetooth side of the app, small enough to fake in JVM tests: scans
 * for the Z407 service, connects and subscribes to the response
 * characteristic.
 */
fun interface Dialer {
    /**
     * Blocks until connected. Notifications may reach [events] before it
     * returns.
     */
    @Throws(IOException::class, InterruptedException::class)
    fun dial(events: LinkEvents): Link
}
