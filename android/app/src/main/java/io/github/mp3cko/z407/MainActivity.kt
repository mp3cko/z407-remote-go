package io.github.mp3cko.z407

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.app.AlertDialog
import android.bluetooth.BluetoothAdapter
import android.bluetooth.BluetoothManager
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.graphics.Color
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.webkit.JavascriptInterface
import android.webkit.ServiceWorkerClient
import android.webkit.ServiceWorkerController
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import androidx.webkit.WebViewAssetLoader
import java.io.ByteArrayInputStream

/**
 * The whole app: a full-screen WebView showing the same page the desktop app
 * serves (copied from internal/server/index.html at build time). The page's
 * requests to /api are answered here from native Bluetooth code, so the page
 * needs no Android-specific code.
 */
class MainActivity : Activity() {
    private companion object {
        const val REQUEST_PERMISSIONS = 1
        const val REQUEST_ENABLE_BLUETOOTH = 2
        const val REQUEST_NOTIFICATIONS = 3
        const val HOST = WebViewAssetLoader.DEFAULT_DOMAIN // appassets.androidplatform.net
    }

    private lateinit var remote: Remote
    private lateinit var assets: WebViewAssetLoader
    private var askedPermissions = false
    private var askedToEnable = false
    private var askedNotifications = false
    private var dialog: AlertDialog? = null

    // JavaScript runs only for the page bundled with the app; answer() keeps
    // every request of the WebView away from the network.
    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        remote = Remote.get(this)
        assets = WebViewAssetLoader.Builder()
            .addPathHandler("/", WebViewAssetLoader.AssetsPathHandler(this))
            .build()

        val web = WebView(this)
        web.settings.javaScriptEnabled = true
        web.settings.domStorageEnabled = true // the theme picked on the remote
        web.setBackgroundColor(getColor(R.color.page))
        web.overScrollMode = View.OVER_SCROLL_NEVER
        web.webViewClient = object : WebViewClient() {
            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest) = answer(request)

            // Links leaving the app (the footer's credits) open in the browser.
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                if (request.url.host == HOST) return false
                try {
                    startActivity(Intent(Intent.ACTION_VIEW, request.url))
                } catch (_: ActivityNotFoundException) {
                    // no browser installed
                }
                return true
            }
        }
        // The page registers a service worker when it can; its requests do not
        // pass through the WebViewClient and must not reach the network either.
        ServiceWorkerController.getInstance().setServiceWorkerClient(object : ServiceWorkerClient() {
            override fun shouldInterceptRequest(request: WebResourceRequest) = answer(request)
        })

        val root = FrameLayout(this)
        root.setBackgroundColor(getColor(R.color.page))
        root.addView(web, FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT)
        keepClearOfSystemBars(root)
        setContentView(root)
        matchSystemBarIcons(!night())
        web.addJavascriptInterface(Bars(root, web), "Z407Bars")

        web.loadUrl("https://$HOST/")
        remote.onQuit = { runOnUiThread { finishAndRemoveTask() } }
    }

    /** Answers every request of the page; nothing of it reaches the network. */
    private fun answer(request: WebResourceRequest): WebResourceResponse? {
        val url = request.url
        if (url.host != HOST) return null
        val path = url.path ?: "/"
        if (path.startsWith("/api/")) {
            val r = remote.api.handle(request.method, path, url.getQueryParameter("list"), url.getQueryParameter("after"))
            return WebResourceResponse(
                r.contentType.substringBefore(';'), "utf-8", r.status, r.reason,
                mapOf("Cache-Control" to "no-store"), ByteArrayInputStream(r.body.toByteArray()),
            )
        }
        val asset = if (path == "/") url.buildUpon().path("/index.html").build() else url
        return assets.shouldInterceptRequest(asset)
            ?: WebResourceResponse("text/plain", "utf-8", 404, "Not Found", emptyMap(), ByteArrayInputStream(ByteArray(0)))
    }

    /**
     * Android 15 draws apps behind the status and navigation bars. The page
     * does not know about those, so the WebView is inset instead.
     */
    private fun keepClearOfSystemBars(root: View) {
        root.setOnApplyWindowInsetsListener { v, insets ->
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                val i = insets.getInsets(
                    WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout() or WindowInsets.Type.ime()
                )
                v.setPadding(i.left, i.top, i.right, i.bottom)
            } else {
                @Suppress("DEPRECATION")
                v.setPadding(
                    insets.systemWindowInsetLeft, insets.systemWindowInsetTop,
                    insets.systemWindowInsetRight, insets.systemWindowInsetBottom,
                )
            }
            insets
        }
    }

    private fun night() =
        resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK == Configuration.UI_MODE_NIGHT_YES

    /** Dark icons on a light page, light icons on a dark one. */
    private fun matchSystemBarIcons(lightPage: Boolean) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val light = WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS or
                WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS
            window.insetsController?.setSystemBarsAppearance(if (lightPage) light else 0, light)
        } else {
            val light = View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR or
                (if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O_MR1) View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR else 0)
            @Suppress("DEPRECATION")
            window.decorView.systemUiVisibility =
                if (lightPage) window.decorView.systemUiVisibility or light
                else window.decorView.systemUiVisibility and light.inv()
        }
    }

    /**
     * Called by the page with its background colour whenever the theme
     * changes: the system bars and what shows around the page take it on.
     */
    private inner class Bars(private val root: View, private val web: WebView) {
        @JavascriptInterface
        fun color(css: String) {
            val c = try {
                Color.parseColor(css.trim())
            } catch (_: IllegalArgumentException) {
                return
            }
            runOnUiThread {
                root.setBackgroundColor(c)
                web.setBackgroundColor(c)
                @Suppress("DEPRECATION") // ignored from Android 15, where the root shows behind the bars
                window.statusBarColor = c
                @Suppress("DEPRECATION")
                window.navigationBarColor = c
                matchSystemBarIcons(Color.luminance(c) > .5f)
            }
        }
    }

    override fun onStart() {
        super.onStart()
        when {
            Permissions.granted(this) -> connect()
            !askedPermissions -> {
                askedPermissions = true
                requestPermissions(Permissions.needed, REQUEST_PERMISSIONS)
            }
        }
    }

    override fun onDestroy() {
        if (!isChangingConfigurations) remote.onQuit = null
        dialog?.dismiss()
        super.onDestroy()
    }

    // Only called once the permissions are granted (asking to switch
    // Bluetooth on needs BLUETOOTH_CONNECT on Android 12+).
    @SuppressLint("MissingPermission")
    private fun connect() {
        // The service connects and keeps the remote working when the app
        // leaves the screen; its notification needs a permission of its own.
        RemoteService.start(this)
        remote.opened()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !askedNotifications &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            askedNotifications = true
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQUEST_NOTIFICATIONS)
        }
        val adapter = (getSystemService(Context.BLUETOOTH_SERVICE) as BluetoothManager?)?.adapter
        if (adapter != null && !adapter.isEnabled && !askedToEnable) {
            askedToEnable = true
            try {
                @Suppress("DEPRECATION")
                startActivityForResult(Intent(BluetoothAdapter.ACTION_REQUEST_ENABLE), REQUEST_ENABLE_BLUETOOTH)
            } catch (_: RuntimeException) {
                // no such screen on this device; the page shows "Not connected"
            }
        }
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != REQUEST_PERMISSIONS) return
        if (Permissions.granted(this)) connect() else explainRefusal()
    }

    /** Says plainly what does not work without the permission, and how to grant it. */
    private fun explainRefusal() {
        // After "Don't ask again" the system no longer shows its prompt; only the settings help then.
        val canAskAgain = Permissions.needed.any { shouldShowRequestPermissionRationale(it) }
        val builder = AlertDialog.Builder(this)
            .setTitle(R.string.permission_title)
            .setMessage(if (Permissions.usesLocation) R.string.permission_location else R.string.permission_nearby)
            .setNegativeButton(R.string.permission_close, null)
        if (canAskAgain) {
            builder.setPositiveButton(R.string.permission_retry) { _, _ ->
                requestPermissions(Permissions.needed, REQUEST_PERMISSIONS)
            }
        } else {
            builder.setPositiveButton(R.string.permission_settings) { _, _ ->
                askedPermissions = false // check again when the user comes back
                startActivity(
                    Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", packageName, null))
                )
            }
        }
        dialog = builder.show()
    }
}
