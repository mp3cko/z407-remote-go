// Command z407 serves a web remote control for the Logitech Z407 speakers.
package main

// The Windows program gets its icon from rsrc_windows_amd64.syso, a resource
// file the linker picks up by its name. It is built from z407.ico, which
// internal/server/icongen.go draws.
//
//go:generate go run github.com/akavel/rsrc@v0.10.2 -ico z407.ico -arch amd64 -o rsrc_windows_amd64.syso

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mp3cko/z407-remote-anywhere/internal/appwin"
	"github.com/mp3cko/z407-remote-anywhere/internal/ble"
	"github.com/mp3cko/z407-remote-anywhere/internal/logx"
	"github.com/mp3cko/z407-remote-anywhere/internal/macwin"
	"github.com/mp3cko/z407-remote-anywhere/internal/peers"
	"github.com/mp3cko/z407-remote-anywhere/internal/power"
	"github.com/mp3cko/z407-remote-anywhere/internal/server"
	"github.com/mp3cko/z407-remote-anywhere/internal/speaker"
	"golang.org/x/term"
)

const (
	shutdownWait = 5 * time.Second
	// portWait is how long a busy port is tried again. A copy whose window
	// was just closed still holds the port until the browser has exited,
	// which takes a moment: one started again at once waits for it.
	portWait    = 5 * time.Second
	defaultPort = peers.DefaultPort
	// sleepGrace is how long the computer is held from sleeping once the
	// speakers were let go: macOS disconnects in the background, and the
	// speakers must hear of it before the Bluetooth radio dozes off.
	sleepGrace = 2 * time.Second
)

// backend is a speaker connection manager that can be run in the background.
type backend interface {
	server.Speaker
	Run(ctx context.Context)
	// Found reports whether the connection attempt under way has seen the speakers.
	Found() bool
}

// sleeper is a backend that lets go of the speakers while the computer sleeps.
type sleeper interface {
	Sleep()
	Wake()
}

// logLines is how many log lines the log viewer of the remote can show.
const logLines = 500

// deps are the replaceable collaborators of runWith.
type deps struct {
	sp     backend
	dial   func(network, address string) (net.Conn, error)
	ifaces func() []ifaceAddrs // nil = system interfaces
	ready  func(addr net.Addr) // optional; called once serving, after the window was opened
	// waitLimit bounds the wait for background goroutines at shutdown
	// (default shutdownWait).
	waitLimit time.Duration
	// portWait bounds the wait for a busy port (default portWait).
	portWait time.Duration
	// win opens the remote in a window on this machine; nil = never.
	win windowOpener
	log *logx.Logger // nil = colored output on stderr
	// earlyExit and grace override the window timing (defaults: earlyExit,
	// server.DefaultGrace).
	earlyExit time.Duration
	grace     time.Duration
	// configDir is the user's configuration directory, where the peer list
	// is saved; nil = keep the list in memory only.
	configDir func() (string, error)
	// takeover is the template for asking peers to release the speakers;
	// Peers and Connected are filled in. The zero value uses the defaults.
	takeover      peers.Takeover
	takeoverEvery time.Duration                  // 0 = peers.CheckInterval
	localIPs      func() []net.IP                // nil = none
	lookup        func(string) ([]net.IP, error) // nil = names never resolve
	// power reports the computer going to sleep and waking up; nil = never.
	power func(sleep, wake func()) error
	// sleepGrace overrides sleepGrace.
	sleepGrace time.Duration
}

func main() {
	// On macOS the main thread is kept for the remote's window.
	macwin.Main(realMain)
}

func realMain() {
	term := os.Stdout
	setupOutput(logMode(os.Args[1:]))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		failExit(err, os.Stdout, os.Stdin, runtime.GOOS, isTerminal(os.Stdin) && os.Stdout == term)
		if os.Stdout != term {
			// Logging elsewhere: tell the terminal too, if there is one.
			fmt.Fprintln(term, errorBanner(err))
		}
		alertFailure(err)
		os.Exit(1)
	}
}

// appWindow opens the remote in a browser's app window, or on macOS in a
// window of the program's own.
func appWindow() appwin.System {
	sys := appwin.Real(runtime.GOOS)
	if macwin.Available() {
		sys.Native = func(url string) (appwin.Process, error) {
			return macwin.Open(url, appwin.WindowTitle, appwin.WindowWidth, appwin.WindowHeight, server.Icon())
		}
	}
	return sys
}

// failExit prints the error banner. On Windows, where a double-clicked
// console window closes immediately, it also waits for ENTER when interactive.
func failExit(err error, out io.Writer, in io.Reader, goos string, tty bool) {
	fmt.Fprintln(out, errorBanner(err))
	if goos == "windows" && tty {
		fmt.Fprint(out, "\nPress ENTER to exit...")
		_, _ = bufio.NewReader(in).ReadString('\n')
	}
}

// isTerminal reports whether f is a terminal. A character device is not
// enough: the macOS app started from Finder has /dev/null as its output.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// run parses args and serves until ctx is cancelled or the window showing
// the remote is closed.
func run(ctx context.Context, args []string, stdout io.Writer) error {
	lg := logx.New(os.Stderr)
	return runWith(ctx, args, stdout, deps{
		sp:     speaker.NewManager(ble.NewDialer(), speaker.WithLogger(lg.Logf)),
		dial:   net.Dial,
		ifaces: systemIfaces,
		win:    appWindow(),
		log:    lg,

		configDir: os.UserConfigDir,
		localIPs:  systemIPs,
		lookup:    lookupHost,
		power: func(sleep, wake func()) error {
			power.NoNap()
			return power.Watch(sleep, wake)
		},
	})
}

func runWith(ctx context.Context, args []string, stdout io.Writer, d deps) error {
	fs := flag.NewFlagSet("z407", flag.ContinueOnError)
	fs.SetOutput(stdout)
	ip := fs.String("ip", "0.0.0.0", "Host IP to bind to")
	port := fs.Int("port", defaultPort, "Port to bind to")
	noWindow := fs.Bool("no-window", false, "Do not open the remote in a window; run until interrupted (Ctrl+C)")
	peerList := fs.String("peers", "", "Other devices running this app, as host or host:port, comma-separated.\n"+
		"Replaces the list saved from the remote's page for this run")
	logTo := fs.String("log", "", "Where the output goes: \"terminal\" or \"file\" ("+logPath()+").\n"+
		"Default: terminal; the Windows GUI build, which has no terminal: file")
	install := fs.Bool("install", false, "Linux: add the remote, with its icon, to the applications menu, and exit")
	uninstall := fs.Bool("uninstall", false, "Linux: remove the remote from the applications menu, and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *install || *uninstall {
		return runLauncher(stdout, *install)
	}
	if *logTo != "" && *logTo != logTerminal && *logTo != logFile {
		return fmt.Errorf("--log: %q is not %q or %q", *logTo, logTerminal, logFile)
	}
	lg := d.log
	if lg == nil {
		lg = logx.New(os.Stderr)
	}

	history := logx.NewHistory(logLines)
	lg.Record(history)
	notice := &server.Notice{}

	store := d.openPeers(lg)
	peersGiven := false
	fs.Visit(func(f *flag.Flag) { peersGiven = peersGiven || f.Name == "peers" })
	if peersGiven {
		if err := store.Fix(*peerList); err != nil {
			return fmt.Errorf("--peers: %w", err)
		}
	}

	wait := d.portWait
	if wait <= 0 {
		wait = portWait
	}
	ln, err := listen(net.JoinHostPort(*ip, strconv.Itoa(*port)), wait, lg)
	if err != nil {
		return err
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port
	fmt.Fprint(stdout, banner(d.addrs(), actualPort, *ip))

	ctx, cancelBg := context.WithCancel(ctx)
	defer cancelBg()

	presence := server.NewPresence(d.grace, func() {
		lg.Infof("The remote's page was closed, stopping")
		cancelBg()
	})
	// The page's "Ask devices" key; a press while one waits adds nothing.
	again := make(chan struct{}, 1)
	askAgain := func() {
		select {
		case again <- struct{}{}:
		default:
		}
	}
	handler := server.New(d.sp, server.WithLogger(lg), server.WithPresence(presence), server.WithPeers(store),
		server.WithAgain(askAgain),
		server.WithAddrs(func() []string { return webURLs(d.addrs(), actualPort, *ip) }),
		server.WithNotice(notice), server.WithHistory(history))
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Long-lived requests (the pages' event streams) end with the app.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); d.sp.Run(ctx) }()
	// Asleep, this computer would hold the speakers without using them or
	// answering the other devices that ask for them.
	if s, ok := d.sp.(sleeper); ok && d.power != nil {
		grace := d.sleepGrace
		if grace <= 0 {
			grace = sleepGrace
		}
		err := d.power(func() {
			s.Sleep()
			time.Sleep(grace)
		}, s.Wake)
		if err != nil {
			lg.Errorf("The speakers will stay connected while the computer sleeps: %v", err)
		}
	}

	tk := d.takeover
	tk.Peers = func() []string { list, _ := store.List(); return list }
	tk.Connected = func() bool { connected, _ := d.sp.Status(); return connected }
	tk.Found = d.sp.Found
	tk.Released = func() bool { phase, _ := d.sp.Progress(); return phase == speaker.PhaseReleased }
	tk.Note = notice.Set
	tk.Again = again
	if tk.IsSelf == nil {
		tk.IsSelf = peers.SelfCheck(actualPort, d.ips, d.resolve)
	}
	if tk.Log == nil {
		tk.Log = lg.Logf
	}
	wg.Add(1)
	go func() { defer wg.Done(); tk.Run(ctx, d.takeoverEvery) }()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	closeWindow := func() {}
	if !*noWindow && d.win != nil {
		early := d.earlyExit
		if early <= 0 {
			early = earlyExit
		}
		w := window{win: d.win, log: lg, presence: presence, quit: cancelBg, early: early}
		closeWindow = w.open(ctx, localURL(*ip, actualPort), actualPort)
	}
	if d.ready != nil {
		d.ready(ln.Addr())
	}

	var result error
	select {
	case <-ctx.Done():
	case result = <-serveErr:
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		_ = srv.Close()
	}
	if errors.Is(result, http.ErrServerClosed) {
		result = nil
	}
	cancelBg() // also needed when Serve failed while ctx is still live
	closeWindow()
	limit := d.waitLimit
	if limit <= 0 {
		limit = shutdownWait
	}
	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(limit):
		fmt.Fprintf(stdout, "shutdown: background tasks still running after %v, exiting anyway\n", limit)
	}
	fmt.Fprint(stdout, goodbye())
	return result
}

// openPeers opens the saved peer list. Problems are logged and leave the
// app running with an empty list.
func (d deps) openPeers(lg *logx.Logger) *peers.Store {
	path := ""
	if d.configDir != nil {
		dir, err := d.configDir()
		if err != nil {
			lg.Errorf("No configuration directory (%v): the list of other computers will not be saved", err)
		} else {
			path = filepath.Join(dir, "z407", "config.json")
		}
	}
	store, err := peers.Open(path, defaultPort)
	if err != nil {
		lg.Errorf("Could not read the list of other computers: %v", err)
	}
	return store
}

func (d deps) ips() []net.IP {
	if d.localIPs == nil {
		return nil
	}
	return d.localIPs()
}

func (d deps) resolve(host string) ([]net.IP, error) {
	if d.lookup == nil {
		return nil, errors.New("no resolver")
	}
	return d.lookup(host)
}

// systemIPs returns the addresses of this machine's network interfaces.
func systemIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

// lookupHost resolves a peer name, without letting a slow resolver stall
// the takeover for long.
func lookupHost(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), peers.PeerTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP
	}
	return ips, nil
}

// getIP returns the local IP used for outbound traffic. UDP "connect" sends
// nothing; it only makes the OS choose the main interface.
func getIP(dial func(network, address string) (net.Conn, error)) string {
	conn, err := dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return "127.0.0.1"
	}
	return addr.IP.String()
}

func banner(addrs []hostAddr, port int, host string) string {
	b := "\n" + strings.Repeat("#", 60) + "\n"
	b += logx.Green("   REMOTE CONTROL AVAILABLE") + "\n"
	b += logx.Green("   ACCESS FROM ANY BROWSER:") + "\n\n"
	if host != "0.0.0.0" {
		b += urlLines([]hostAddr{{IP: host}}, port) + "\n"
		b += fmt.Sprintf("   (Bound specifically to: %s)\n", host)
	} else {
		b += urlLines(addrs, port) + "\n"
		if len(addrs) > 1 {
			b += "   Several networks found: use the one on the same Wi-Fi as your phone\n"
			b += "   (10.x addresses are often a VPN adapter).\n"
		}
	}
	b += strings.Repeat("#", 60) + "\n\n"
	return b
}

// addrs returns the addresses to advertise, best guess first.
func (d deps) addrs() []hostAddr {
	var ifs []ifaceAddrs
	if d.ifaces != nil {
		ifs = d.ifaces()
	}
	return pickAddrs(ifs, getIP(d.dial))
}

func goodbye() string {
	return "\n" + strings.Repeat("=", 50) + "\n" +
		"👋 Goodbye! Thanks for using this app.\n" +
		strings.Repeat("=", 50) + "\n\n"
}

func errorBanner(err error) string {
	return "\n\n" + strings.Repeat("!", 60) + "\n" +
		logx.Red("CRITICAL ERROR: The app failed to start.") + "\n" +
		fmt.Sprintf("Error details: %v\n", err) +
		strings.Repeat("!", 60) + "\n" +
		"\nPOSSIBLE CAUSES:\n" +
		"1. The port is occupied by another program.\n" +
		"2. Missing permissions (bluetooth/network).\n" +
		"3. Check if you have another instance running.\n" +
		portHint(err)
}

// listen listens on addr, trying a busy port again for up to wait.
func listen(addr string, wait time.Duration, lg *logx.Logger) (net.Listener, error) {
	deadline := time.Now().Add(wait)
	logged := false
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil || !addrInUse(err) || !time.Now().Before(deadline) {
			return ln, err
		}
		if !logged {
			lg.Infof("Port %s is in use, waiting up to %v for it to be free (a copy that was just closed may still be stopping)", addr, wait)
			logged = true
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// addrInUse reports whether err says the address is taken.
func addrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use") ||
		strings.Contains(err.Error(), "Only one usage of each socket address")
}

// portHint explains the common "address already in use" case, including the
// WSL quirk where the Windows exe and the Linux binary share localhost ports.
func portHint(err error) string {
	if !addrInUse(err) {
		return ""
	}
	return "\nThe port is still in use after waiting " + portWait.String() + ". Most likely another copy of\n" +
		"Z407 Remote is running on it: one started with --no-window, one whose\n" +
		"remote is open in a browser tab, or (on WSL) the Windows build.\n" +
		"Stop that copy, or start this one on another port, e.g.:  z407 --port 2408\n"
}
