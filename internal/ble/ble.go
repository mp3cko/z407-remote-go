// Package ble implements a speaker.Dialer on top of tinygo.org/x/bluetooth.
package ble

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/protocol"
	"github.com/mp3cko/z407-remote-go/internal/speaker"
)

// ErrNotFound is returned when no speaker advertising the Z407 service is seen.
var ErrNotFound = fmt.Errorf("ble: %w", speaker.ErrNotFound)

const (
	defaultScanTimeout  = 10 * time.Second
	defaultPollInterval = 2 * time.Second
	defaultStopRetry    = 50 * time.Millisecond
)

// scanResult is one advertisement seen while scanning.
type scanResult struct {
	addr       any // opaque, passed back to radio.connect
	hasService bool
}

type radio interface {
	enable() error
	// scan blocks, calling cb for each advertisement, until stopScan is called.
	scan(cb func(scanResult)) error
	stopScan() error
	// connect must arrange for onDisconnect to run when the link drops.
	connect(addr any, onDisconnect func()) (device, error)
}

type device interface {
	// characteristics discovers the service and returns its characteristics keyed by UUID.
	characteristics(service string, uuids []string) (map[string]characteristic, error)
	disconnect() error
	// connected reports whether the link is still up (polled: not every
	// platform reports remote disconnects through the callback).
	connected() (bool, error)
}

type characteristic interface {
	enableNotifications(cb func([]byte)) error
	disableNotifications() error
	writeWithoutResponse([]byte) error
}

// Dialer connects to a Z407 over BLE.
type Dialer struct {
	r            radio
	scanTimeout  time.Duration
	pollInterval time.Duration // how often to check the link is still up
	prePoll      func()        // test hook, see link.prePoll
	stopRetry    time.Duration // retry period for stopScan racing scan start
}

// NewDialer returns a Dialer using the system Bluetooth adapter.
func NewDialer() *Dialer {
	return &Dialer{r: newRealRadio(), scanTimeout: defaultScanTimeout, pollInterval: defaultPollInterval, stopRetry: defaultStopRetry}
}

var _ speaker.Dialer = (*Dialer)(nil)

// link is an established GATT connection.
type link struct {
	dev      device
	cmd      characteristic
	resp     characteristic
	mu       sync.Mutex
	devMu    sync.Mutex
	prePoll  func() // test hook: runs after a tick, before taking devMu
	closed   bool
	stop     chan struct{}
	stopOnce sync.Once
}

// Dial scans for the speaker, connects, and wires notifications to h.
func (d *Dialer) Dial(ctx context.Context, h speaker.Handler) (speaker.Link, error) {
	if err := d.r.enable(); err != nil {
		return nil, fmt.Errorf("ble: %w: %w", speaker.ErrNoBluetooth, err)
	}
	addr, err := d.find(ctx)
	if err != nil {
		return nil, err
	}

	l := &link{stop: make(chan struct{})}
	var once sync.Once
	fire := func() {
		once.Do(func() {
			l.mu.Lock()
			closed := l.closed
			l.mu.Unlock()
			if !closed && h.OnDisconnect != nil {
				h.OnDisconnect()
			}
		})
	}
	dev, err := d.r.connect(addr, fire)
	if err != nil {
		return nil, connectError(err)
	}

	chars, err := dev.characteristics(protocol.ServiceUUID, []string{protocol.CommandUUID, protocol.ResponseUUID})
	if err != nil {
		_ = dev.disconnect()
		return nil, fmt.Errorf("ble: discover services: %w", connectError(err))
	}
	cmd, resp := chars[protocol.CommandUUID], chars[protocol.ResponseUUID]
	if cmd == nil || resp == nil {
		_ = dev.disconnect()
		return nil, errors.New("ble: speaker is missing the command/response characteristic")
	}
	notify := h.OnNotify
	if notify == nil {
		notify = func([]byte) {}
	}
	if err := resp.enableNotifications(notify); err != nil {
		_ = dev.disconnect()
		return nil, fmt.Errorf("ble: enable notifications: %w", err)
	}
	l.dev, l.cmd, l.resp = dev, cmd, resp
	l.prePoll = d.prePoll
	if d.pollInterval > 0 {
		go l.poll(d.pollInterval, fire)
	}
	return l, nil
}

// poll reports a remote disconnect via fire; some platforms (Linux) never
// call the connection-state callback. Query errors are treated as transient.
func (l *link) poll(every time.Duration, fire func()) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			if l.prePoll != nil {
				l.prePoll()
			}
			l.devMu.Lock()
			select {
			case <-l.stop: // Close ran first and may have released the device
				l.devMu.Unlock()
				return
			default:
			}
			ok, err := l.dev.connected()
			l.devMu.Unlock()
			if err == nil && !ok {
				fire()
				return
			}
		}
	}
}

// stopAndWait stops the scan, retrying while it fails (stopScan can race
// scan start and be a no-op), until it succeeds or the scan returns on its own.
func (d *Dialer) stopAndWait(done <-chan error) {
	retry := d.stopRetry
	if retry <= 0 {
		retry = defaultStopRetry
	}
	for {
		if d.r.stopScan() == nil {
			<-done
			return
		}
		select {
		case <-done:
			return
		case <-time.After(retry):
		}
	}
}

// find scans until a device advertising the Z407 service shows up.
func (d *Dialer) find(ctx context.Context) (any, error) {
	scanCtx, cancel := context.WithTimeout(ctx, d.scanTimeout)
	defer cancel()

	found := make(chan any, 1)
	done := make(chan error, 1)
	go func() {
		done <- d.r.scan(func(res scanResult) {
			if !res.hasService {
				return
			}
			select {
			case found <- res.addr:
				_ = d.r.stopScan()
			default:
			}
		})
	}()

	select {
	case addr := <-found:
		<-done
		return addr, nil
	case err := <-done:
		// Clear a stale "scan in progress" marker left by early library errors.
		_ = d.r.stopScan()
		select {
		case addr := <-found:
			return addr, nil
		default:
		}
		if err == nil {
			err = ErrNotFound
		}
		return nil, fmt.Errorf("ble: scan: %w", err)
	case <-scanCtx.Done():
		d.stopAndWait(done)
		select {
		case addr := <-found:
			return addr, nil
		default:
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrNotFound
	}
}

// Write sends a command without response.
func (l *link) Write(cmd protocol.Command) error {
	return l.cmd.writeWithoutResponse(cmd)
}

// Close disconnects. It does not trigger OnDisconnect.
func (l *link) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	l.stopOnce.Do(func() { close(l.stop) })
	// devMu keeps disconnect from overlapping an in-flight connected() poll
	// (on Windows that would touch a released COM object). It is never held
	// while firing OnDisconnect, so Close may be called from that handler.
	l.devMu.Lock()
	defer l.devMu.Unlock()
	_ = l.resp.disableNotifications()
	return l.dev.disconnect()
}

// connectError adds a hint for the Windows "Catastrophic failure" error.
func connectError(err error) error {
	s := err.Error()
	if strings.Contains(s, "2147418113") || strings.Contains(strings.ToLower(s), "catastroph") || strings.Contains(strings.ToLower(s), "catastrófico") {
		return fmt.Errorf("ble: connect: %w (Windows Bluetooth is confused: go to Settings > Bluetooth, remove the Z407 and re-pair it)", err)
	}
	return fmt.Errorf("ble: connect: %w", err)
}
