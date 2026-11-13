package ble

import (
	"errors"
	"sync"

	"github.com/mp3cko/z407-remote-anywhere/internal/protocol"
	bt "tinygo.org/x/bluetooth"
)

// realRadio adapts the tinygo bluetooth default adapter. Not unit-testable without hardware.
type realRadio struct {
	mu       sync.Mutex
	enabled  bool
	enableFn func() error
	a        *bt.Adapter
}

func newRealRadio() *realRadio {
	return &realRadio{a: bt.DefaultAdapter, enableFn: bt.DefaultAdapter.Enable}
}

// enable only remembers success, so a later retry recovers once Bluetooth is on.
func (r *realRadio) enable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.enabled {
		return nil
	}
	if err := r.enableFn(); err != nil {
		return err
	}
	r.enabled = true
	return nil
}

func (r *realRadio) scan(cb func(scanResult)) error {
	svc, err := bt.ParseUUID(protocol.ServiceUUID)
	if err != nil {
		return err
	}
	return r.a.Scan(func(_ *bt.Adapter, res bt.ScanResult) {
		cb(scanResult{addr: res.Address, hasService: res.HasServiceUUID(svc)})
	})
}

func (r *realRadio) stopScan() error { return r.a.StopScan() }

func (r *realRadio) connect(addr any, onDisconnect func()) (device, error) {
	a, ok := addr.(bt.Address)
	if !ok {
		return nil, errors.New("ble: bad address type")
	}
	r.a.SetConnectionStateHandler(func(_ bt.Device, st bt.ConnectionState) {
		if !st.Connected {
			onDisconnect()
		}
	})
	d, err := r.a.Connect(a, bt.ConnectionParams{})
	if err != nil {
		return nil, err
	}
	return realDevice{d}, nil
}

type realDevice struct{ d bt.Device }

func (d realDevice) disconnect() error { return d.d.Disconnect() }

func (d realDevice) connected() (bool, error) { return d.d.Connected() }

func (d realDevice) characteristics(service string, uuids []string) (map[string]characteristic, error) {
	su, err := bt.ParseUUID(service)
	if err != nil {
		return nil, err
	}
	svcs, err := d.d.DiscoverServices([]bt.UUID{su})
	if err != nil {
		return nil, err
	}
	if len(svcs) == 0 {
		return nil, errors.New("service not found")
	}
	cu := make([]bt.UUID, len(uuids))
	for i, s := range uuids {
		if cu[i], err = bt.ParseUUID(s); err != nil {
			return nil, err
		}
	}
	chars, err := svcs[0].DiscoverCharacteristics(cu)
	if err != nil {
		return nil, err
	}
	out := make(map[string]characteristic, len(chars))
	for _, c := range chars {
		out[c.UUID().String()] = &realChar{c}
	}
	return out, nil
}

type realChar struct{ c bt.DeviceCharacteristic }

func (c *realChar) enableNotifications(cb func([]byte)) error {
	return c.c.EnableNotifications(cb)
}

func (c *realChar) disableNotifications() error { return c.c.EnableNotifications(nil) }

func (c *realChar) writeWithoutResponse(p []byte) error {
	_, err := c.c.WriteWithoutResponse(p)
	return err
}
