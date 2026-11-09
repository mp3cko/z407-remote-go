// Package speaker manages the BLE connection to a Logitech Z407 and exposes
// its remote-control commands and keeps the connection alive.
package speaker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/protocol"
)

// ErrNotConnected is returned by commands when the speakers are not connected.
var ErrNotConnected = errors.New("speakers not connected")

const (
	defaultRetry     = 5 * time.Second
	defaultHeartbeat = 5 * time.Second
	volumeStep       = 5
)

// Handler receives events from a Link.
type Handler struct {
	OnNotify     func(data []byte)
	OnDisconnect func()
}

// Link is an established connection to the speaker.
type Link interface {
	// Write sends cmd as a GATT write without response to CommandUUID.
	Write(cmd protocol.Command) error
	Close() error
}

// Dialer scans for the Z407 service, connects and subscribes to ResponseUUID.
type Dialer interface {
	Dial(ctx context.Context, h Handler) (Link, error)
}

// WithOnConnect registers a callback run after every successful connection.
func WithOnConnect(f func()) Option { return func(m *Manager) { m.onConnect = f } }

// Option configures a Manager.
type Option func(*Manager)

// WithRetryInterval sets the wait between failed connection attempts.
func WithRetryInterval(d time.Duration) Option {
	return func(m *Manager) {
		if d > 0 {
			m.retry = d
		}
	}
}

// WithHeartbeat sets how often a connected manager re-checks its state.
func WithHeartbeat(d time.Duration) Option {
	return func(m *Manager) {
		if d > 0 {
			m.heartbeat = d
		}
	}
}

// WithLogger sets a printf-style logger. By default nothing is logged.
func WithLogger(f func(format string, args ...any)) Option {
	return func(m *Manager) { m.log = f }
}

// Manager owns the connection lifecycle and the command API. It is safe for
// concurrent use.
type Manager struct {
	dialer    Dialer
	retry     time.Duration
	heartbeat time.Duration
	log       func(format string, args ...any)
	onConnect func()
	lastFail  string

	wake chan struct{} // signalled when the current connection is lost

	mu        sync.Mutex
	link      Link
	connected bool
	dropped   bool // current attempt lost its connection
	pendingKA bool // keep-alive received before Dial returned
	attempt   int  // generation of the current attempt; stale callbacks are ignored
	volume    int
}

// NewManager returns a Manager that connects through d once Run is called.
func NewManager(d Dialer, opts ...Option) *Manager {
	m := &Manager{
		dialer:    d,
		retry:     defaultRetry,
		heartbeat: defaultHeartbeat,
		wake:      make(chan struct{}, 1),
		volume:    50,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// logFail logs a connection failure unless it repeats the previous one, so a
// permanently missing adapter or absent speakers do not flood the console.
// connectOnce runs on a single goroutine, so lastFail needs no lock.
func (m *Manager) logFail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if msg == m.lastFail {
		return
	}
	m.lastFail = msg
	m.logf("%s (retrying every %s, repeats hidden)", msg, m.retry)
}

func (m *Manager) logf(format string, args ...any) {
	if m.log != nil {
		m.log(format, args...)
	}
}

// Run keeps the speakers connected until ctx is cancelled, then closes the link.
func (m *Manager) Run(ctx context.Context) {
	defer m.shutdown()
	for ctx.Err() == nil {
		if m.Connected() {
			m.wait(ctx, m.heartbeat, true)
			continue
		}
		if !m.connectOnce(ctx) {
			m.wait(ctx, m.retry, false)
		}
	}
}

func (m *Manager) wait(ctx context.Context, d time.Duration, wakeable bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	var wake <-chan struct{}
	if wakeable {
		wake = m.wake
	}
	select {
	case <-ctx.Done():
	case <-t.C:
	case <-wake:
	}
}

func (m *Manager) shutdown() {
	m.mu.Lock()
	l := m.link
	m.link, m.connected = nil, false
	m.attempt++
	m.mu.Unlock()
	if l != nil {
		_ = l.Close()
	}
}

func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// connectOnce makes one dial + handshake attempt and reports success.
func (m *Manager) connectOnce(ctx context.Context) bool {
	m.mu.Lock()
	old := m.link
	m.link, m.connected, m.dropped, m.pendingKA = nil, false, false, false
	m.attempt++
	id := m.attempt
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	select { // discard wake-ups belonging to the previous connection
	case <-m.wake:
	default:
	}

	l, err := m.dialer.Dial(ctx, Handler{
		OnNotify:     func(data []byte) { m.onNotify(id, data) },
		OnDisconnect: func() { m.onDisconnect(id) },
	})
	if err != nil {
		m.logFail("Dial failed: %v", err)
		return false
	}
	if err := l.Write(protocol.Handshake); err != nil {
		m.logFail("Handshake failed: %v", err)
		_ = l.Close()
		return false
	}

	m.mu.Lock()
	if m.attempt != id || m.dropped {
		m.mu.Unlock()
		_ = l.Close()
		return false
	}
	m.link, m.connected = l, true
	pending := m.pendingKA
	m.pendingKA = false
	m.mu.Unlock()

	if pending {
		m.reply(l)
	}
	m.lastFail = ""
	m.logf("Connected to Z407")
	if m.onConnect != nil {
		m.onConnect()
	}
	return true
}

func (m *Manager) reply(l Link) {
	if err := l.Write(protocol.KeepAliveReply); err != nil {
		m.fail(l)
	}
}

func (m *Manager) onNotify(id int, data []byte) {
	m.mu.Lock()
	if id != m.attempt {
		m.mu.Unlock()
		return
	}
	switch {
	case bytes.Equal(data, protocol.NotifyKeepAlive):
		l := m.link
		if l == nil {
			m.pendingKA = true
		}
		m.mu.Unlock()
		if l != nil {
			m.reply(l)
		}
	case bytes.Equal(data, protocol.NotifyReady):
		m.connected, m.dropped = true, false
		m.mu.Unlock()
	default:
		m.mu.Unlock()
	}
}

func (m *Manager) onDisconnect(id int) {
	m.mu.Lock()
	if id == m.attempt {
		m.connected, m.dropped = false, true
		m.signal()
	}
	m.mu.Unlock()
}

// fail marks the connection over l as lost.
func (m *Manager) fail(l Link) {
	m.mu.Lock()
	if m.link == l {
		m.connected = false
		m.signal()
	}
	m.mu.Unlock()
}

// Connected reports whether the speakers are currently connected.
func (m *Manager) Connected() bool {
	c, _ := m.Status()
	return c
}

// Status returns the connection state and the estimated volume (0-100).
func (m *Manager) Status() (connected bool, volume int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected, m.volume
}

func (m *Manager) send(cmd protocol.Command) error {
	m.mu.Lock()
	l := m.link
	ok := m.connected && l != nil
	m.mu.Unlock()
	if !ok {
		return ErrNotConnected
	}
	if err := l.Write(cmd); err != nil {
		m.fail(l)
		return fmt.Errorf("write %x: %w", []byte(cmd), err)
	}
	return nil
}

func (m *Manager) adjustVolume(cmd protocol.Command, delta int) error {
	if err := m.send(cmd); err != nil {
		return err
	}
	m.mu.Lock()
	m.volume = min(100, max(0, m.volume+delta))
	m.mu.Unlock()
	return nil
}

func (m *Manager) VolumeUp() error   { return m.adjustVolume(protocol.VolumeUp, volumeStep) }
func (m *Manager) VolumeDown() error { return m.adjustVolume(protocol.VolumeDown, -volumeStep) }
func (m *Manager) PlayPause() error  { return m.send(protocol.PlayPause) }
func (m *Manager) InputBluetooth() error {
	return m.send(protocol.InputBluetooth)
}
func (m *Manager) InputAux() error      { return m.send(protocol.InputAux) }
func (m *Manager) InputUSB() error      { return m.send(protocol.InputUSB) }
func (m *Manager) BluetoothPair() error { return m.send(protocol.BluetoothPair) }
func (m *Manager) FactoryReset() error  { return m.send(protocol.FactoryReset) }
func (m *Manager) BassUp() error        { return m.send(protocol.BassUp) }
func (m *Manager) BassDown() error      { return m.send(protocol.BassDown) }
func (m *Manager) NextTrack() error     { return m.send(protocol.NextTrack) }
func (m *Manager) PrevTrack() error     { return m.send(protocol.PrevTrack) }
