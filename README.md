# Z407 Remote

A small web remote for the Logitech Z407 speakers. It runs on a computer with Bluetooth, talks to the speakers over Bluetooth Low Energy, and serves a control page you can open from any phone or tablet on the same network.

## Features

- Speaker volume, bass, play/pause and track skip
- Input selection (Bluetooth, AUX, USB), pairing and factory reset
- Single static binary for Windows, Linux and macOS

## Install

Download the archive for your platform from the [releases page](https://github.com/mp3cko/z407-remote-go/releases), extract it and run `z407` (`z407.exe` on Windows).

Or build it yourself (Go 1.26.1 or newer):

```bash
make build     # dist/z407 for the current OS
make windows   # dist/Z407_Control_Windows.exe (cross-compiles from any OS)
make test
```

### Platform notes

- **Linux:** BlueZ is required.
- **macOS:** build natively on a Mac (cgo and the Xcode command line tools are required; it cannot be cross-compiled). Allow Bluetooth for your terminal when prompted.
- **Windows:** Windows 10 or 11 with a Bluetooth LE adapter. Make sure the speakers are already paired in Windows settings.

## Usage

1. Run `z407`.
2. Open the address it prints (for example `http://192.168.1.50:5000`) on your phone.

By default it listens on all interfaces on port 5000. To change that:

```bash
z407 --ip 127.0.0.1 --port 8080
```

## Troubleshooting

- The speakers accept only one BLE controller at a time. Disconnect any phone that is connected to them.
- If your phone cannot load the page, check the firewall on the computer, and if you use a VPN enable "Allow LAN access" or split tunneling.
- On Windows, clicking inside the console window pauses the program; press Enter to resume it.
- If connecting keeps failing, try in order: power-cycle the speakers, toggle Bluetooth on the computer, unpair and re-pair the speakers, restart the computer.

## Acknowledgements

- The speaker protocol comes from [freundTech's reverse engineering work](https://github.com/freundTech/logi-z407-reverse-engineering).
- This project began as a Go rewrite of [Androrama's Z407 remote web app](https://github.com/androrama/Logitech-Z407-Remote-Control-Web-App---Windows); thanks for the idea and the original.

## Disclaimer

This is an unofficial project and is not affiliated with, endorsed by, or connected to Logitech. Use it at your own risk.

## License

[MIT](LICENSE)
