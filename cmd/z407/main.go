// Command z407 serves a web remote control for the Logitech Z407 speakers.
package main

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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/appwin"
	"github.com/mp3cko/z407-remote-go/internal/ble"
	"github.com/mp3cko/z407-remote-go/internal/logx"
	"github.com/mp3cko/z407-remote-go/internal/server"
	"github.com/mp3cko/z407-remote-go/internal/speaker"
)

const shutdownWait = 5 * time.Second

// backend is a speaker connection manager that can be run in the background.
type backend interface {
	server.Speaker
	Run(ctx context.Context)
}

// deps are the replaceable collaborators of runWith.
type deps struct {
	sp     backend
	dial   func(network, address string) (net.Conn, error)
	ifaces func() []ifaceAddrs // nil = system interfaces
	ready  func(addr net.Addr) // optional; called once serving, after the window was opened
	// waitLimit bounds the wait for background goroutines at shutdown
	// (default shutdownWait).
	waitLimit time.Duration
	// win opens the remote in a window on this machine; nil = never.
	win windowOpener
	log *logx.Logger // nil = colored output on stderr
	// earlyExit and grace override the window timing (defaults: earlyExit,
	// server.DefaultGrace).
	earlyExit time.Duration
	grace     time.Duration
}

func main() {
	setupOutput()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		failExit(err, os.Stdout, os.Stdin, runtime.GOOS, isTerminal(os.Stdin))
		alertFailure(err)
		os.Exit(1)
	}
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

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// run parses args and serves until ctx is cancelled or the window showing
// the remote is closed.
func run(ctx context.Context, args []string, stdout io.Writer) error {
	lg := logx.New(os.Stderr)
	return runWith(ctx, args, stdout, deps{
		sp:     speaker.NewManager(ble.NewDialer(), speaker.WithLogger(lg.Logf)),
		dial:   net.Dial,
		ifaces: systemIfaces,
		win:    appwin.Real(runtime.GOOS),
		log:    lg,
	})
}

func runWith(ctx context.Context, args []string, stdout io.Writer, d deps) error {
	fs := flag.NewFlagSet("z407", flag.ContinueOnError)
	fs.SetOutput(stdout)
	ip := fs.String("ip", "0.0.0.0", "Host IP to bind to")
	port := fs.Int("port", 5000, "Port to bind to")
	noWindow := fs.Bool("no-window", false, "Do not open the remote in a window; run until interrupted (Ctrl+C)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(*ip, strconv.Itoa(*port)))
	if err != nil {
		return err
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port
	fmt.Fprint(stdout, banner(d.addrs(), actualPort, *ip))

	lg := d.log
	if lg == nil {
		lg = logx.New(os.Stderr)
	}
	ctx, cancelBg := context.WithCancel(ctx)
	defer cancelBg()

	presence := server.NewPresence(d.grace, func() {
		lg.Infof("The remote's page was closed, stopping")
		cancelBg()
	})
	srv := &http.Server{
		Handler:           server.New(d.sp, server.WithLogger(lg), server.WithPresence(presence)),
		ReadHeaderTimeout: 10 * time.Second,
		// Long-lived requests (the pages' event streams) end with the app.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); d.sp.Run(ctx) }()

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

// portHint explains the common "address already in use" case, including the
// WSL quirk where the Windows exe and the Linux binary share localhost ports.
func portHint(err error) string {
	if !errors.Is(err, syscall.EADDRINUSE) && !strings.Contains(err.Error(), "address already in use") &&
		!strings.Contains(err.Error(), "Only one usage of each socket address") {
		return ""
	}
	return "\nPort already in use. Try another one, e.g.:  z407 --port 5001\n" +
		"(On WSL the Windows build may already be running on this port.)\n"
}
