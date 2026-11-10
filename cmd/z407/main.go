// Command z407 serves a web remote control for the Logitech Z407 speakers.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
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

	"github.com/mp3cko/z407-remote-go/internal/ble"
	"github.com/mp3cko/z407-remote-go/internal/media"
	"github.com/mp3cko/z407-remote-go/internal/server"
	"github.com/mp3cko/z407-remote-go/internal/speaker"
)

const (
	green  = "\033[92m"
	yellow = "\033[93m"
	red    = "\033[91m"
	reset  = "\033[0m"

	shutdownWait = 5 * time.Second
)

// backend is a speaker connection manager that can be run in the background.
type backend interface {
	server.Speaker
	Run(ctx context.Context)
}

// deps are the replaceable collaborators of runWith.
type deps struct {
	sp   backend
	keys media.Presser
	dial func(network, address string) (net.Conn, error)
	// connected fires when the speakers connect; the access URL is shown
	// once, on the first signal.
	connected <-chan struct{}
	ifaces    func() []ifaceAddrs // nil = system interfaces
	ready     func(addr net.Addr) // optional; called once listening
	// waitLimit bounds the wait for background goroutines at shutdown
	// (default shutdownWait).
	waitLimit time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		failExit(err, os.Stdout, os.Stdin, runtime.GOOS, isTerminal(os.Stdin))
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

// run parses args and serves until ctx is cancelled.
func run(ctx context.Context, args []string, stdout io.Writer) error {
	up := make(chan struct{}, 1)
	onConnect := func() {
		select {
		case up <- struct{}{}:
		default:
		}
	}
	return runWith(ctx, args, stdout, deps{
		sp:        speaker.NewManager(ble.NewDialer(), speaker.WithLogger(log.Printf), speaker.WithOnConnect(onConnect)),
		keys:      media.New(),
		dial:      net.Dial,
		ifaces:    systemIfaces,
		connected: up,
	})
}

func runWith(ctx context.Context, args []string, stdout io.Writer, d deps) error {
	fs := flag.NewFlagSet("z407", flag.ContinueOnError)
	fs.SetOutput(stdout)
	ip := fs.String("ip", "0.0.0.0", "Host IP to bind to")
	port := fs.Int("port", 5000, "Port to bind to")
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
	if d.ready != nil {
		d.ready(ln.Addr())
	}

	srv := &http.Server{
		Handler:           server.New(d.sp, d.keys),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancelBg := context.WithCancel(ctx)
	defer cancelBg()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); d.sp.Run(ctx) }()
	go func() { defer wg.Done(); remindOnce(ctx, stdout, d.connected, d.addrs, actualPort) }()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

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

// remindOnce prints the access URL once, when the speakers first connect.
func remindOnce(ctx context.Context, w io.Writer, connected <-chan struct{}, addrs func() []hostAddr, port int) {
	select {
	case <-ctx.Done():
	case <-connected: // nil channel blocks until ctx is done
		fmt.Fprint(w, reminderText(addrs(), port))
	}
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
	b += green + "   REMOTE CONTROL AVAILABLE" + reset + "\n"
	b += green + "   ACCESS FROM ANY BROWSER:" + reset + "\n\n"
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

func reminderText(addrs []hostAddr, port int) string {
	return "\n" + strings.Repeat("-", 50) + "\n" +
		green + "ℹ️  Z407 CONNECTED. Access from your browser:" + reset + "\n" +
		urlLines(addrs, port) +
		strings.Repeat("-", 50) + "\n\n"
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
		red + "CRITICAL ERROR: The app failed to start." + reset + "\n" +
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
