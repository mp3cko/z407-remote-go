# Z407 Remote

A small remote for the Logitech Z407 speakers. It runs on a computer with Bluetooth, talks to the speakers over Bluetooth Low Energy, shows the remote in its own window, and serves the same page to any phone or tablet on the same network. There is also an [Android app](#android-app) that controls the speakers directly, without a computer.

## Features

- Speaker volume, bass, play/pause and track skip
- Input selection (Bluetooth, AUX, USB), pairing and factory reset
- Opens as an app window on the computer; phones on the network can add it to their home screen
- Single static binary for Windows, Linux and macOS
- Android app with the same remote, talking to the speakers directly

## Install

Download the archive for your platform from the [releases page](https://github.com/mp3cko/z407-remote-go/releases), extract it and run `z407` (`z407.exe` on Windows).

Or build it yourself (Go 1.26.1 or newer):

```bash
make build          # dist/z407 for the current OS
make cross          # Linux (amd64, arm64) and Windows from any OS, plus macOS when run on a Mac
make windows-amd64  # a single target, e.g. dist/z407_windows_amd64.exe
make test
```

`make help` lists all targets. Windows binaries are built as GUI programs (`-H=windowsgui`), so no console window opens; `make windows-amd64 WINDOWS_CONSOLE=1` builds a console program instead. The macOS targets (`darwin-arm64`, `darwin-amd64`) only work on a Mac, because the Bluetooth backend needs cgo.

### Platform notes

- **Linux:** BlueZ is required.
- **macOS:** build natively on a Mac (cgo and the Xcode command line tools are required; it cannot be cross-compiled). Allow Bluetooth for your terminal when prompted.
- **Windows:** Windows 10 or 11 with a Bluetooth LE adapter. Make sure the speakers are already paired in Windows settings.

## Usage

Run `z407` (double-click it, or start it from a terminal). The remote opens in its own window. Closing the window stops the app.

While the window is open, other devices on the same network can use the remote too: open the address the app prints (for example `http://192.168.1.50:5000`) on your phone. The API has no authentication, so anyone on your network who knows the address can control the speakers, and can also close the app (`POST /api/shutdown`, see [Several computers](#several-computers)).

### The window

- The window is an "app" window of Edge, Chrome, Chromium or Brave, whichever is installed. It uses its own browser profile (in your cache directory, under `z407`), so it does not touch your normal browser.
- If none of these browsers is installed, the remote opens as a tab in your default browser, and the app stops a few seconds after you close the last tab showing it on that computer. Reloading the tab is fine. Tabs on other devices never keep the app running and never stop it. Browsers that discard background tabs to save memory can stop the app this way; use `--no-window` if that bothers you.
- `z407 --no-window` opens nothing and runs until you press Ctrl+C. Use this on a machine without a screen. On Linux this is also what happens when there is no graphical session.
- Ctrl+C in the terminal stops the app in every mode and closes the window.

### Options

By default the app listens on all interfaces on port 5000. To change that:

```bash
z407 --ip 127.0.0.1 --port 8080
```

### Several computers

The speakers accept one Bluetooth LE controller at a time, so a second copy of this app cannot connect while the first one holds the link. To let the copy you started last win, tell each copy where the others run:

- On the remote's page, open "Other computers" below the remote, enter the other machines as `host` or `host:port` separated by commas (for example `living-room-pc, 192.168.1.20:5001`; the port defaults to 5000) and press Save. The list is stored in `z407/config.json` in your configuration directory (`%AppData%` on Windows, `~/.config` on Linux, `~/Library/Application Support` on macOS) and takes effect at once.
- Or start the app with `--peers living-room-pc,192.168.1.20:5001`. This replaces the saved list for that run only; the saved list is not changed and cannot be edited on the page during that run.

When a copy has searched for the speakers for 10 seconds without connecting, it sends `POST /api/shutdown` to every computer on its list. A copy that receives this closes itself (window included) and releases the speakers; the searching copy then connects on one of its next attempts. Each computer is asked at most once per run and a copy never asks itself, so two copies that list each other cannot keep shutting each other down. Computers that are off or not running the app are skipped.

Like the rest of the API, `/api/shutdown` is not authenticated: anyone on your network can close the app with it. They cannot do more than that.

### On your phone

Open the address in the phone's browser and choose "Add to Home screen" from the browser menu. You get an icon that opens the remote without the browser's address bar. It works while the app is running on the computer. (A full offline-capable install needs HTTPS, which a LAN address does not have; the shortcut is all that is needed here.)

### Windows

`z407.exe` opens no console window. Started from a terminal it prints its log there; started by double-click it writes the log to `%LOCALAPPDATA%\z407\z407.log`, and a startup problem (for example the port being in use) is shown in a message box.

## Android app

The Android app shows the same remote and talks to the speakers itself over Bluetooth LE. No computer is involved. It needs Android 8.0 or newer.

Install: download `z407_vX.Y.Z_android.apk` from the [releases page](https://github.com/mp3cko/z407-remote-go/releases) on the phone and open it (Android asks you to allow installing from that source), or from a computer with the phone attached: `adb install z407_vX.Y.Z_android.apk`.

Good to know:

- On first start the app asks for the "Nearby devices" permission (Android 12 and newer) or the Location permission (Android 8 to 11, where Bluetooth scans require it and Location must be switched on). The app does not use your position.
- The app holds the speakers only while it is on screen. When you leave it, it disconnects, so that other controllers can connect.
- If a computer running the desktop app holds the speakers, the phone cannot connect. Open "Other computers" in the app and enter that computer's address: after 10 seconds of searching, the app asks it to close its copy (see [Several computers](#several-computers)) and then connects. Each computer is asked once each time the app comes to the screen. The phone and the computer must be on the same network for this. Nothing can close the phone app remotely.

### Building it

You need JDK 17 and the Android SDK (platform 35 and build-tools 35.0.0; the command line tools are enough). Point `ANDROID_HOME` at the SDK, or write its path to `android/local.properties` as `sdk.dir=/path/to/sdk`.

```bash
cd android
./gradlew test            # unit tests of the protocol, state machine and API (no phone needed)
./gradlew assembleDebug   # app/build/outputs/apk/debug/app-debug.apk
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

`make android` in the repository root runs the first two steps and copies the APK to `dist/z407_android_debug.apk`.

The app has no copy of the remote's page: the build takes `internal/server/index.html` from this repository. `./gradlew assembleRelease -PversionName=1.2.3` builds a release APK; it is signed with the key named by the environment variables `Z407_KEYSTORE_FILE`, `Z407_KEYSTORE_PASSWORD`, `Z407_KEY_ALIAS` and `Z407_KEY_PASSWORD`, or with the debug key when they are not set. The release workflow reads the key from the repository secrets `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS` and `ANDROID_KEY_PASSWORD`.

### Manual test checklist

The Bluetooth side cannot be tested automatically. With a phone and the speakers:

1. Fresh install, first start: the permission prompt appears. Refuse it: a dialog explains what will not work and offers to ask again. Grant it.
2. With Bluetooth switched off, start the app: it offers to switch Bluetooth on.
3. Speakers on, nothing else connected to their control channel: within about 15 seconds the display shows "Connected" and the Signal lamp is green.
4. Volume up and down: the speakers react and the volume estimate moves in steps of 5. Bass up and down, play/pause, next, previous: the speakers react.
5. BT, AUX, USB: the speakers switch input and the lamp above the pressed key lights.
6. Pair: after confirming, the speakers enter pairing mode. (Reset erases the speakers' pairings; test it only if you mean to.)
7. Leave the app open for five minutes: it stays connected (keep-alives are answered).
8. Switch the speakers off: the display shows "Not connected". Switch them on: the app reconnects by itself.
9. Press Home, then check that another controller (for example the desktop app) can now connect. Return to the app after closing the other controller: it reconnects.
10. Rotate the phone and switch the system between light and dark: the remote follows, and the connection survives the rotation.
11. Takeover: run the desktop app on a computer so that it is connected. On the phone enter the computer's address under "Other computers" and save; an invalid entry such as `http://x` is refused with a message. About 10 seconds after the app started searching (at once, if it has searched longer already) the desktop app closes, and shortly after the phone shows "Connected".
12. Start the desktop app again, wait until it is connected, then bring the phone app back to the screen: it takes the speakers over again.
13. Close the phone app completely and reopen it: the saved list is still there.

## Troubleshooting

- The speakers accept only one BLE controller at a time. Disconnect any phone that is connected to them, and see [Several computers](#several-computers) if another computer runs this app.
- If your phone cannot load the page, check the firewall on the computer, and if you use a VPN enable "Allow LAN access" or split tunneling.
- The app window does not open: check the log (on Windows `%LOCALAPPDATA%\z407\z407.log`). You can always open the printed address in any browser.
- On Windows, when started from a terminal, clicking inside the terminal can pause the program; press Enter to resume it.
- If connecting keeps failing, try in order: power-cycle the speakers, toggle Bluetooth on the computer, unpair and re-pair the speakers, restart the computer.

## Acknowledgements

- The speaker protocol comes from [freundTech's reverse engineering work](https://github.com/freundTech/logi-z407-reverse-engineering).
- This project began as a Go rewrite of [Androrama's Z407 remote web app](https://github.com/androrama/Logitech-Z407-Remote-Control-Web-App---Windows); thanks for the idea and the original.

## Disclaimer

This is an unofficial project and is not affiliated with, endorsed by, or connected to Logitech. Use it at your own risk.

## License

[MIT](LICENSE)
