package logx

import (
	"bytes"
	"strings"
	"testing"
)

func TestLevelColors(t *testing.T) {
	for _, c := range []struct {
		fn   func(*Logger)
		want string
	}{
		{func(l *Logger) { l.Infof("hello %d", 1) }, blue + "hello 1" + reset},
		{func(l *Logger) { l.Successf("ok") }, green + "ok" + reset},
		{func(l *Logger) { l.Errorf("bad") }, red + "bad" + reset},
	} {
		var buf bytes.Buffer
		c.fn(NewWriter(&buf, true))
		if !strings.Contains(buf.String(), c.want) {
			t.Errorf("got %q, want it to contain %q", buf.String(), c.want)
		}
	}
}

func TestNoColor(t *testing.T) {
	var buf bytes.Buffer
	NewWriter(&buf, false).Errorf("plain")
	if strings.Contains(buf.String(), "\033") || !strings.Contains(buf.String(), "plain") {
		t.Errorf("unexpected output %q", buf.String())
	}
}

func TestEnabledRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if Enabled(nil) {
		t.Error("NO_COLOR must disable colors")
	}
}

func TestPaintOff(t *testing.T) {
	if got := paint(false, green, "x"); got != "x" {
		t.Errorf("got %q", got)
	}
}
