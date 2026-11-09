package ble

import (
	"errors"
	"testing"
)

func TestEnableRetriesAfterFailure(t *testing.T) {
	calls := 0
	r := &realRadio{enableFn: func() error {
		calls++
		if calls == 1 {
			return errors.New("bluetooth off")
		}
		return nil
	}}
	if err := r.enable(); err == nil {
		t.Fatal("first enable should fail")
	}
	if err := r.enable(); err != nil {
		t.Fatalf("retry should recover: %v", err)
	}
	if err := r.enable(); err != nil || calls != 2 {
		t.Fatalf("success must be cached: err=%v calls=%d", err, calls)
	}
}
