package logx

import (
	"bytes"
	"testing"
	"time"
)

func TestHistory(t *testing.T) {
	h := NewHistory(3)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h.now = func() time.Time { return at }
	if got, last := h.Since(0); len(got) != 0 || last != 0 {
		t.Fatalf("empty: %v, %d", got, last)
	}
	h.Add(Info, "one")
	h.Add(Error, "two")
	got, last := h.Since(0)
	if last != 2 || len(got) != 2 || got[0] != (Entry{1, at, Info, "one"}) || got[1] != (Entry{2, at, Error, "two"}) {
		t.Fatalf("got %+v, %d", got, last)
	}
	if got, last := h.Since(1); last != 2 || len(got) != 1 || got[0].Text != "two" {
		t.Fatalf("since 1: %+v, %d", got, last)
	}
	if got, last := h.Since(2); last != 2 || len(got) != 0 {
		t.Fatalf("since 2: %+v, %d", got, last)
	}

	// The oldest lines drop out; the numbering goes on.
	h.Add(Success, "three")
	h.Add(Info, "four")
	got, last = h.Since(0)
	if last != 4 || len(got) != 3 || got[0].N != 2 || got[2].Text != "four" {
		t.Fatalf("after overflow: %+v, %d", got, last)
	}
	// A reader that fell behind gets what is left.
	if got, _ := h.Since(1); len(got) != 3 {
		t.Fatalf("fell behind: %+v", got)
	}
}

func TestLoggerRecords(t *testing.T) {
	var buf bytes.Buffer
	h := NewHistory(10)
	lg := NewWriter(&buf, true)
	lg.Record(h)
	lg.Errorf("bad %d", 7)
	lg.Successf("fine")
	got, _ := h.Since(0)
	if len(got) != 2 || got[0].Level != Error || got[0].Text != "bad 7" || got[1].Level != Success {
		t.Fatalf("got %+v", got) // the text is kept without colors
	}
	for l, want := range map[Level]string{Info: "info", Success: "ok", Error: "error"} {
		if l.String() != want {
			t.Errorf("%d: %s", l, l)
		}
	}
}
