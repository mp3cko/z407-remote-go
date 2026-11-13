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

	"github.com/mp3cko/z407-remote-anywhere/internal/logx"
	"github.com/mp3cko/z407-remote-anywhere/internal/protocol"
)

// ErrNotConnected is returned by commands when the speakers are not connected.
var ErrNotConnected = errors.New("speakers not connected")

// Errors a Dialer wraps so that the remote can say what is wrong.
var (
	// ErrNotFound: no speaker was seen while scanning.
	ErrNotFound = errors.New("Z407 speaker not found")
	// ErrNoBluetooth: the Bluetooth adapter is missing or switched off.
	ErrNoBluetooth = errors.New("Bluetooth is not available")
)

// What the remote shows for a failed connection attempt.
const (
	problemNotFound    = "Speakers not found"
	problemNoBluetooth = "Bluetooth is off or missing"
	problemConnect     = "Could not connect"
	problemNoAnswer    = "Speakers did not answer"
	problemLost        = "Connection lost"
)

// dialProblem names a failed Dial for the remote's display.
func dialProblem(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return problemNotFound
	case errors.Is(err, ErrNoBluetooth):
		return problemNoBluetooth
	}
	return problemConnect
}

const (
	defaultRetry = 5 * time.Second
	// softFails is how many failed attempts in a row pass as a hiccup: a
	// warning in the log and "searching" on the display. A copy started
	// right after another one closed, or one taking the speakers over,
	// often fails once or twice while the speakers let go of the other.
	softFails        = 2
	defaultHeartbeat = 5 * time.Second
	volumeStep       = 5
)

// Handler receives events from a Dialer and its Link.
type Handler struct {
	// OnFound is called during Dial, once the speakers have been seen and
	// the connection to them begins.
	OnFound      func()
	OnNotify     func(data []byte)
	OnDisconnect func()
}

// Phases of a connection attempt, as reported by Progress.
const (
	PhaseSearching  = "searching"  // looking for the speakers
	PhaseConnecting = "connecting" // found them, setting up the connection
	// PhaseReleased: Release was called. Nothing is attempted until Reconnect.
	PhaseReleased = "released"
)

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

// WithLogger sets a leveled printf-style logger. By default nothing is logged.
func WithLogger(f func(level logx.Level, format string, args ...any)) Option {
	return func(m *Manager) { m.log = f }
}

// Manager owns the connection lifecycle and the command API. It is safe for
// concurrent use.
type Manager struct {
	dialer    Dialer
	retry     time.Duration
	heartbeat time.Duration
	log       func(level logx.Level, format string, args ...any)
	lastFail  string
	fails     int // failed attempts since the last connection

	wake chan struct{} // signalled when the current connection is lost
	turn chan struct{} // signalled by Release and Reconnect

	mu        sync.Mutex
	released  bool               // between Release and Reconnect
	cancel    context.CancelFunc // ends the attempt under way
	link      Link
	connected bool
	phase     string // of the attempt under way; "" = none is
	found     bool   // the latest attempt has seen the speakers
	problem   string // what went wrong with the last attempt; "" = nothing
	dropped   bool   // current attempt lost its connection
	pendingKA bool   // keep-alive received before Dial returned
	attempt   int    // generation of the current attempt; stale callbacks are ignored
	volume    int
}

// NewManager returns a Manager that connects through d once Run is called.
func NewManager(d Dialer, opts ...Option) *Manager {
	m := &Manager{
		dialer:    d,
		retry:     defaultRetry,
		heartbeat: defaultHeartbeat,
		wake:      make(chan struct{}, 1),
		turn:      make(chan struct{}, 1),
		volume:    50,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// attemptFailed ends a connection attempt that did not work out, and logs why
// unless it repeats the previous failure, so a permanently missing adapter
// or absent speakers do not flood the console. The first few failures in a
// row are a warning and leave the display searching; later ones, and a
// missing Bluetooth adapter, are an error shown as problem.
// connectOnce runs on a single goroutine, so lastFail and fails need no lock.
func (m *Manager) attemptFailed(problem, format string, args ...any) {
	m.fails++
	hard := m.fails > softFails || problem == problemNoBluetooth
	m.mu.Lock()
	if hard {
		m.phase, m.problem = "", problem
	} else {
		m.phase, m.problem = PhaseSearching, ""
	}
	m.mu.Unlock()
	msg := fmt.Sprintf(format, args...)
	if key := fmt.Sprint(hard, msg); key != m.lastFail {
		m.lastFail = key
		if hard {
			m.logf(logx.Error, "%s (retrying every %s, repeats hidden)", msg, m.retry)
		} else {
			m.logf(logx.Warning, "%s (retrying in %s)", msg, m.retry)
		}
	}
}

func (m *Manager) logf(level logx.Level, format string, args ...any) {
	if m.log != nil {
		m.log(level, format, args...)
	}
}

// Run keeps the speakers connected until ctx is cancelled, then closes the
// link. Between Release and Reconnect it leaves the speakers alone.
func (m *Manager) Run(ctx context.Context) {
	defer m.shutdown()
	for ctx.Err() == nil {
		if m.isReleased() {
			select {
			case <-ctx.Done():
			case <-m.turn:
			}
			continue
		}
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
	case <-m.turn:
	}
}

// Release lets go of the speakers, so that another controller can connect
// (they accept one at a time), and leaves them alone until Reconnect.
func (m *Manager) Release() {
	m.mu.Lock()
	if m.released {
		m.mu.Unlock()
		return
	}
	m.released = true
	l, cancel := m.link, m.cancel
	m.link, m.connected, m.phase, m.problem, m.found = nil, false, "", "", false
	m.attempt++
	m.mu.Unlock()
	if cancel != nil {
		cancel() // a dial in progress is abandoned
	}
	if l != nil {
		_ = l.Close()
	}
	m.logf(logx.Info, "Another device asked for the speakers: disconnected until you reconnect")
	m.turned()
}

// Reconnect ends a Release: Run connects again.
func (m *Manager) Reconnect() {
	m.mu.Lock()
	was := m.released
	m.released = false
	m.mu.Unlock()
	if was {
		m.logf(logx.Info, "Reconnecting")
		m.turned()
	}
}

func (m *Manager) isReleased() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.released
}

func (m *Manager) turned() {
	select {
	case m.turn <- struct{}{}:
	default:
	}
}

func (m *Manager) shutdown() {
	m.mu.Lock()
	l := m.link
	m.link, m.connected, m.phase = nil, false, ""
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.mu.Lock()
	if m.released {
		m.mu.Unlock()
		return false
	}
	m.cancel = cancel
	old := m.link
	m.link, m.connected, m.dropped, m.pendingKA = nil, false, false, false
	m.phase, m.found = PhaseSearching, false
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
		OnFound:      func() { m.onFound(id) },
		OnNotify:     func(data []byte) { m.onNotify(id, data) },
		OnDisconnect: func() { m.onDisconnect(id) },
	})
	if err != nil {
		if m.isReleased() {
			return false // abandoned, not failed
		}
		m.attemptFailed(dialProblem(err), "Dial failed: %v", err)
		return false
	}
	if err := l.Write(protocol.Handshake); err != nil {
		_ = l.Close()
		m.attemptFailed(problemNoAnswer, "Handshake failed: %v", err)
		return false
	}

	m.mu.Lock()
	if m.attempt != id || m.dropped {
		if !m.released {
			m.phase, m.problem = "", problemLost
		}
		m.mu.Unlock()
		_ = l.Close()
		return false
	}
	m.link, m.connected = l, true
	m.phase, m.problem = "", ""
	pending := m.pendingKA
	m.pendingKA = false
	m.mu.Unlock()

	if pending {
		m.reply(l)
	}
	m.lastFail, m.fails = "", 0
	m.logf(logx.Success, "Connected to Z407")
	return true
}

func (m *Manager) onFound(id int) {
	m.mu.Lock()
	if id == m.attempt && m.phase == PhaseSearching {
		m.phase, m.found = PhaseConnecting, true
	}
	m.mu.Unlock()
}

// Found reports whether the latest connection attempt has seen the
// speakers. They can be seen and still refuse the connection.
func (m *Manager) Found() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.found
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

// Progress tells more about a missing connection: the phase of the attempt
// under way (PhaseSearching, PhaseConnecting, or "" between attempts), and
// what went wrong with the last one ("" when nothing did). The problem stays
// until a connection succeeds. After Release the phase is PhaseReleased.
func (m *Manager) Progress() (phase, problem string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.released {
		return PhaseReleased, ""
	}
	return m.phase, m.problem
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
