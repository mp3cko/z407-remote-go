# Testing the Android app by hand

The Bluetooth side cannot be tested automatically. With a phone and the speakers:

1. Fresh install, first start: the permission prompt appears. Refuse it: a dialog explains what will not work and offers to ask again. Grant it. On Android 13 and newer a second prompt asks for notifications: allow it, and the notification "Z407" appears.
2. With Bluetooth switched off, start the app: it offers to switch Bluetooth on.
3. Speakers on, nothing else connected to their control channel: within about 15 seconds the display shows "Connected" and the Signal lamp is green.
4. Volume up and down: the speakers react and the volume estimate moves in steps of 5. Bass up and down, play/pause, next, previous: the speakers react.
5. BT, AUX, USB: the speakers switch input and the lamp above the pressed key lights.
6. Pair: after confirming, the speakers enter pairing mode. (Reset erases the speakers' pairings; test it only if you mean to.)
7. Leave the app open for five minutes: it stays connected (keep-alives are answered).
8. Switch the speakers off: the display shows "Not connected". Switch them on: the app reconnects by itself.
9. Press Home, then turn the screen off for a minute: the notification "Z407 · Connected" stays, and its three keys (volume down, play/pause, volume up) work; the volume in its text follows. Tap it: the app opens. Expand the notification and press Quit: the app closes, the notification goes, and another controller (for example the desktop app) can now connect. Open the app again and swipe it away in the recent apps: the same happens.
10. Rotate the phone and switch the system between light and dark: the remote follows, and the connection survives the rotation.
11. Takeover: run the desktop app on a computer so that it is connected. On the phone press Devices, enter the computer's address and press Add; an invalid entry such as `http://x` is refused with a message. About 3 seconds after the app started searching (at once, if it has searched longer already) the display shows "Asking ... to release the speakers", then "... released the speakers", the desktop app shows "Disconnected" and a Reconnect key, and shortly after the phone shows "Connected".
12. Press Reconnect on the desktop app (the phone's address must be on its Devices list): at once the phone shows "Disconnected" and the Reconnect key, and the desktop app connects. Press Reconnect on the phone: it takes the speakers over again at once.
13. Startup: with the desktop app connected and the phone on its Devices list, close the phone app (swipe it from the recent apps) and open it again: right away, without a wait, the display shows "Asking ... to release the speakers", then "... released the speakers", and the phone connects.
14. Open the address shown under "Web address" in a browser on a computer: the remote appears and controls the speakers, also while the phone's screen is off. Swipe the app away in the recent apps: the address no longer answers.
15. While the phone shows "Disconnected", rotate it, press Home and return: it stays disconnected, and its notification says "Disconnected". Press Reconnect: it takes the speakers over.
16. Close the phone app completely and reopen it: the saved list is still there.
17. Press the display: the log opens and shows what happened so far (searching, connecting, the takeover); new lines appear by themselves. Close it with the cross or a tap outside.
18. Press Theme and pick Arctic: the remote, the page and the status and navigation bars turn light, with dark icons in the bars. Pick Graphite: the bars turn dark with light icons. Close the app completely and reopen it: Graphite is still chosen.
