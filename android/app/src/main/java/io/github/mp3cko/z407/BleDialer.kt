package io.github.mp3cko.z407

import android.annotation.SuppressLint
import android.bluetooth.BluetoothAdapter
import android.bluetooth.BluetoothDevice
import android.bluetooth.BluetoothGatt
import android.bluetooth.BluetoothGattCallback
import android.bluetooth.BluetoothGattCharacteristic
import android.bluetooth.BluetoothGattDescriptor
import android.bluetooth.BluetoothManager
import android.bluetooth.BluetoothProfile
import android.bluetooth.BluetoothStatusCodes
import android.bluetooth.le.ScanCallback
import android.bluetooth.le.ScanFilter
import android.bluetooth.le.ScanResult
import android.bluetooth.le.ScanSettings
import android.content.Context
import android.os.Build
import android.os.ParcelUuid
import io.github.mp3cko.z407.core.Dialer
import io.github.mp3cko.z407.core.Link
import io.github.mp3cko.z407.core.LinkEvents
import io.github.mp3cko.z407.core.Protocol
import java.io.IOException
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit

/**
 * The Bluetooth LE transport: finds the Z407 by its service UUID, connects,
 * and subscribes to the response characteristic. Android's GATT API is
 * asynchronous; this class turns it into the blocking [Dialer] the
 * platform-independent [io.github.mp3cko.z407.core.SpeakerManager] drives.
 *
 * Cannot be unit-tested without a phone and the speakers.
 */
@SuppressLint("MissingPermission") // checked in dial(); a revoked permission surfaces as SecurityException
class BleDialer(context: Context) : Dialer {
    private val context = context.applicationContext

    companion object {
        const val SCAN_TIMEOUT_MS = 10_000L
        const val CONNECT_TIMEOUT_MS = 15_000L
        const val WRITE_TIMEOUT_MS = 3_000L
        val SERVICE: UUID = UUID.fromString(Protocol.SERVICE_UUID)
        val COMMAND: UUID = UUID.fromString(Protocol.COMMAND_UUID)
        val RESPONSE: UUID = UUID.fromString(Protocol.RESPONSE_UUID)
        /** Client Characteristic Configuration descriptor: switches notifications on. */
        val CCCD: UUID = UUID.fromString("00002902-0000-1000-8000-00805f9b34fb")
    }

    override fun dial(events: LinkEvents): Link {
        if (!Permissions.granted(context)) throw IOException("Bluetooth permission not granted")
        val adapter = (context.getSystemService(Context.BLUETOOTH_SERVICE) as BluetoothManager?)?.adapter
            ?: throw IOException("this device has no Bluetooth")
        if (!adapter.isEnabled) throw IOException("Bluetooth is switched off")
        try {
            val device = find(adapter)
            events.onFound()
            val link = GattLink(events)
            link.connect(device)
            return link
        } catch (e: SecurityException) {
            throw IOException("Bluetooth permission was withdrawn", e)
        }
    }

    /** Scans until a device advertising the Z407 service shows up. */
    private fun find(adapter: BluetoothAdapter): BluetoothDevice {
        val scanner = adapter.bluetoothLeScanner ?: throw IOException("Bluetooth is switched off")
        val found = LinkedBlockingQueue<Any>() // a BluetoothDevice or an IOException
        val callback = object : ScanCallback() {
            override fun onScanResult(callbackType: Int, result: ScanResult) {
                found.offer(result.device)
            }

            override fun onScanFailed(errorCode: Int) {
                found.offer(IOException("scan failed (code $errorCode)"))
            }
        }
        val filter = ScanFilter.Builder().setServiceUuid(ParcelUuid(SERVICE)).build()
        val settings = ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build()
        scanner.startScan(listOf(filter), settings, callback)
        try {
            return when (val r = found.poll(SCAN_TIMEOUT_MS, TimeUnit.MILLISECONDS)) {
                is BluetoothDevice -> r
                is IOException -> throw r
                else -> throw IOException("Z407 speaker not found")
            }
        } finally {
            try {
                scanner.stopScan(callback)
            } catch (_: RuntimeException) {
                // Bluetooth was switched off meanwhile: the scan is gone anyway
            }
        }
    }

    /** One GATT connection. */
    private inner class GattLink(private val events: LinkEvents) : BluetoothGattCallback(), Link {
        private val ready = CountDownLatch(1)
        private val writeLock = Any()
        private val writeDone = LinkedBlockingQueue<Int>()
        // Events are handed to this thread: the manager answers a keep-alive
        // with a write, which waits for a GATT callback and so must not run
        // on the thread that delivers GATT callbacks.
        private val notifier: ExecutorService = Executors.newSingleThreadExecutor()

        @Volatile private var gatt: BluetoothGatt? = null
        @Volatile private var command: BluetoothGattCharacteristic? = null
        @Volatile private var setupError: String? = null
        @Volatile private var up = false // set up and not yet disconnected
        @Volatile private var closed = false

        fun connect(device: BluetoothDevice) {
            val g = device.connectGatt(context, false, this, BluetoothDevice.TRANSPORT_LE)
                ?: throw IOException("could not start connecting")
            gatt = g
            try {
                if (!ready.await(CONNECT_TIMEOUT_MS, TimeUnit.MILLISECONDS)) throw IOException("connecting timed out")
                setupError?.let { throw IOException(it) }
            } catch (e: Exception) { // including InterruptedException: the app is stopping
                close()
                throw e
            }
        }

        private fun failSetup(why: String) {
            setupError = why
            ready.countDown()
        }

        override fun onConnectionStateChange(g: BluetoothGatt, status: Int, newState: Int) {
            when (newState) {
                BluetoothProfile.STATE_CONNECTED ->
                    if (status != BluetoothGatt.GATT_SUCCESS || !g.discoverServices()) {
                        failSetup("connect failed (status $status)")
                    }
                BluetoothProfile.STATE_DISCONNECTED -> {
                    val wasUp = up
                    up = false
                    writeDone.offer(BluetoothGatt.GATT_FAILURE) // release a write in progress
                    if (!wasUp) {
                        failSetup("disconnected while connecting (status $status)")
                    } else if (!closed) {
                        deliver { events.onDisconnect() }
                    }
                }
            }
        }

        override fun onServicesDiscovered(g: BluetoothGatt, status: Int) {
            val service = g.getService(SERVICE)
            val cmd = service?.getCharacteristic(COMMAND)
            val resp = service?.getCharacteristic(RESPONSE)
            val cccd = resp?.getDescriptor(CCCD)
            if (status != BluetoothGatt.GATT_SUCCESS || cmd == null || resp == null || cccd == null) {
                failSetup("speaker is missing the command/response characteristic (status $status)")
                return
            }
            command = cmd
            if (!g.setCharacteristicNotification(resp, true) || !writeDescriptor(g, cccd)) {
                failSetup("could not enable notifications")
            }
        }

        @Suppress("DEPRECATION")
        private fun writeDescriptor(g: BluetoothGatt, d: BluetoothGattDescriptor): Boolean {
            val value = BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                return g.writeDescriptor(d, value) == BluetoothStatusCodes.SUCCESS
            }
            d.value = value
            return g.writeDescriptor(d)
        }

        override fun onDescriptorWrite(g: BluetoothGatt, d: BluetoothGattDescriptor, status: Int) {
            if (status == BluetoothGatt.GATT_SUCCESS) {
                up = true
                ready.countDown()
            } else {
                failSetup("could not enable notifications (status $status)")
            }
        }

        // Android 13+ calls this one...
        override fun onCharacteristicChanged(g: BluetoothGatt, c: BluetoothGattCharacteristic, value: ByteArray) {
            if (c.uuid == RESPONSE) {
                val copy = value.copyOf()
                deliver { events.onNotify(copy) }
            }
        }

        // ...older versions this one.
        @Deprecated("Deprecated in Java")
        @Suppress("DEPRECATION")
        override fun onCharacteristicChanged(g: BluetoothGatt, c: BluetoothGattCharacteristic) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) return
            val copy = c.value?.copyOf() ?: return
            if (c.uuid == RESPONSE) deliver { events.onNotify(copy) }
        }

        override fun onCharacteristicWrite(g: BluetoothGatt, c: BluetoothGattCharacteristic, status: Int) {
            writeDone.offer(status)
        }

        private fun deliver(event: () -> Unit) {
            try {
                notifier.execute(event)
            } catch (_: RuntimeException) {
                // closed meanwhile: nobody is listening any more
            }
        }

        /**
         * A write without response. Android accepts one GATT operation at a
         * time, so writes are serialised and each waits until the stack has
         * taken it.
         */
        @Suppress("DEPRECATION")
        override fun write(cmd: ByteArray) {
            synchronized(writeLock) {
                val g = gatt
                val c = command
                if (g == null || c == null || !up || closed) throw IOException("not connected")
                writeDone.clear()
                val started = try {
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                        g.writeCharacteristic(c, cmd, BluetoothGattCharacteristic.WRITE_TYPE_NO_RESPONSE) ==
                            BluetoothStatusCodes.SUCCESS
                    } else {
                        c.writeType = BluetoothGattCharacteristic.WRITE_TYPE_NO_RESPONSE
                        c.value = cmd
                        g.writeCharacteristic(c)
                    }
                } catch (e: SecurityException) {
                    throw IOException("Bluetooth permission was withdrawn", e)
                }
                if (!started) throw IOException("the Bluetooth stack refused the write")
                val status = try {
                    writeDone.poll(WRITE_TIMEOUT_MS, TimeUnit.MILLISECONDS)
                } catch (e: InterruptedException) {
                    Thread.currentThread().interrupt()
                    throw IOException("interrupted")
                } ?: throw IOException("write timed out")
                if (status != BluetoothGatt.GATT_SUCCESS) throw IOException("write failed (status $status)")
            }
        }

        override fun close() {
            closed = true
            up = false
            notifier.shutdown()
            val g = gatt ?: return
            gatt = null
            try {
                g.disconnect()
                g.close()
            } catch (_: RuntimeException) {
                // permission withdrawn or Bluetooth off: the link is gone anyway
            }
        }
    }
}
