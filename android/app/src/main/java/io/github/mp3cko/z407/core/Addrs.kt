package io.github.mp3cko.z407.core

import java.net.Inet4Address
import java.net.NetworkInterface
import java.net.SocketException

/** The addresses other devices on the network can reach this one at. */
object Addrs {
    /** IPv4 addresses of the network interfaces that are up, Wi-Fi first; mobile data is left out. */
    fun lan(): List<String> = try {
        NetworkInterface.getNetworkInterfaces().toList()
            .filter { it.isUp && !it.isLoopback }
            .sortedBy { if (it.name.startsWith("wlan")) 0 else 1 }
            .flatMap { it.inetAddresses.toList() }
            .filter { it is Inet4Address && it.isSiteLocalAddress }
            .mapNotNull { it.hostAddress }
            .distinct()
    } catch (_: SocketException) {
        emptyList()
    }
}
