package peers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mp3cko/z407-remote-go/internal/logx"
)

const (
	// SearchTimeout is how long this instance looks for the speakers on its
	// own before it asks its peers to release them.
	SearchTimeout = 10 * time.Second
	// PeerTimeout bounds the request to one peer; a computer that is switched
	// off must not hold up the others.
	PeerTimeout = 2 * time.Second
	// CheckInterval is how often Run looks at the connection state.
	CheckInterval = time.Second
)

// Takeover asks peers to quit when the speakers cannot be found. Every peer
// is asked at most once per run: two instances that list each other would
// otherwise keep shutting each other down. Set the exported fields before use.
type Takeover struct {
	Peers     func() []string // current list, "host:port"
	Connected func() bool     // are the speakers connected?
	// IsSelf reports whether peer is this very instance, which must never be
	// asked. nil = nothing is.
	IsSelf func(peer string) bool
	// Post sends the shutdown request and describes the answer. nil = HTTP.
	Post func(ctx context.Context, peer string) (answer string, err error)
	Log  func(level logx.Level, format string, args ...any)
	// Timeout and PeerTimeout default to SearchTimeout and PeerTimeout.
	Timeout     time.Duration
	PeerTimeout time.Duration

	searching bool
	since     time.Time       // start of the current search
	asked     map[string]bool // peers dealt with in this run
}

func (t *Takeover) logf(level logx.Level, format string, args ...any) {
	if t.Log != nil {
		t.Log(level, format, args...)
	}
}

// Check looks at the state at time now and, when the search has lasted the
// whole timeout, asks every peer not asked before. Run calls it regularly;
// it must not be called concurrently.
func (t *Takeover) Check(ctx context.Context, now time.Time) {
	if t.Connected() {
		t.searching = false
		return
	}
	if !t.searching {
		t.searching, t.since = true, now
	}
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = SearchTimeout
	}
	if now.Sub(t.since) < timeout {
		return
	}
	for _, peer := range t.Peers() {
		if ctx.Err() != nil {
			return
		}
		if t.asked[peer] {
			continue
		}
		if t.asked == nil {
			t.asked = map[string]bool{}
		}
		t.asked[peer] = true
		if t.IsSelf != nil && t.IsSelf(peer) {
			t.logf(logx.Info, "Other computers: %s is this computer, skipping it", peer)
			continue
		}
		t.ask(ctx, peer)
	}
}

func (t *Takeover) ask(ctx context.Context, peer string) {
	limit := t.PeerTimeout
	if limit <= 0 {
		limit = PeerTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	post := t.Post
	if post == nil {
		post = PostShutdown
	}
	answer, err := post(ctx, peer)
	if err != nil {
		// Normal: that computer is off, or the app is not running there.
		t.logf(logx.Info, "Other computers: %s did not answer (%v)", peer, err)
		return
	}
	t.logf(logx.Info, "Speakers not found; asked %s to release them: %s", peer, answer)
}

// Run calls Check every interval (CheckInterval if 0) until ctx is cancelled.
func (t *Takeover) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = CheckInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		t.Check(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// PostShutdown sends POST /api/shutdown to peer and returns its answer, e.g.
// `200 {"success":true}`.
func PostShutdown(ctx context.Context, peer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+peer+"/api/shutdown", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	return fmt.Sprintf("%d %s", resp.StatusCode, strings.TrimSpace(string(body))), nil
}

// SelfCheck returns an IsSelf function for an instance listening on port.
// A peer is this instance when its port is ours and its host is a loopback
// address or one of this machine's addresses. Names are resolved with
// lookup; a name that does not resolve is not ours.
func SelfCheck(port int, localIPs func() []net.IP, lookup func(host string) ([]net.IP, error)) func(peer string) bool {
	return func(peer string) bool {
		host, p, err := net.SplitHostPort(peer)
		if err != nil || p != fmt.Sprint(port) {
			return false
		}
		var ips []net.IP
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IP{ip}
		} else if ips, err = lookup(host); err != nil {
			return false
		}
		local := localIPs()
		for _, ip := range ips {
			if ip.IsLoopback() {
				return true
			}
			for _, l := range local {
				if l.Equal(ip) {
					return true
				}
			}
		}
		return false
	}
}
