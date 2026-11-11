# Z407 Remote

A small remote for the Logitech Z407 speakers. It runs on a computer with Bluetooth, talks to the speakers over Bluetooth Low Energy, shows the remote in its own window, and serves the same page to any phone or tablet on the same network.

## Features

- Speaker volume, bass, play/pause and track skip
- Input selection (Bluetooth, AUX, USB), pairing and factory reset
- Opens as an app window on the computer; phones on the network can add it to their home screen
- Single static binary for Windows, Linux and macOS

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

While the window is open, other devices on the same network can use the remote too: open the address the app prints (for example `http://192.168.1.50:5000`) on your phone. The API has no authentication, so anyone on your network who knows the address can control the speakers.

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

### On your phone

Open the address in the phone's browser and choose "Add to Home screen" from the browser menu. You get an icon that opens the remote without the browser's address bar. It works while the app is running on the computer. (A full offline-capable install needs HTTPS, which a LAN address does not have; the shortcut is all that is needed here.)

### Windows

`z407.exe` opens no console window. Started from a terminal it prints its log there; started by double-click it writes the log to `%LOCALAPPDATA%\z407\z407.log`, and a startup problem (for example the port being in use) is shown in a message box.

## Troubleshooting

- The speakers accept only one BLE controller at a time. Disconnect any phone that is connected to them.
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
