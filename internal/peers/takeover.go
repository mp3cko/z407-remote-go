package peers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mp3cko/z407-remote-anywhere/internal/logx"
)

const (
	// SearchTimeout is how long this instance looks for the speakers on its
	// own before it asks peers it has not asked yet. Speakers that are free
	// show up within a second or two. The first round of a search session
	// (the app was started, or told to reconnect) does not wait: the user
	// wants the speakers now, and a peer that holds them would only hold
	// up the start.
	SearchTimeout = 3 * time.Second
	// ConnectTimeout replaces SearchTimeout once the speakers have been
	// seen: setting up the connection takes a moment longer, and a peer must
	// not be closed for that.
	ConnectTimeout = 10 * time.Second
	// PeerTimeout bounds the request to one peer; a computer that is switched
	// off must not hold up the others. The peers are asked at the same time,
	// so a round takes at most this long.
	PeerTimeout = 2 * time.Second
	// CheckInterval is how often Run looks at the connection state.
	CheckInterval = time.Second
)

// Takeover asks peers to disconnect. A search session (the app was started,
// or, having released the speakers itself, told to reconnect) begins by
// asking every peer at once, all at the same time, while the speakers are
// searched for; a peer added later is asked when the speakers have not been
// found within the timeout. Every peer is asked at most once per session:
// two instances that list each other would otherwise keep taking the
// speakers from each other. Set the exported fields before use.
type Takeover struct {
	Peers     func() []string // current list, "host:port"
	Connected func() bool     // are the speakers connected?
	// Released reports whether this instance has let go of the speakers for
	// another device. It then asks nobody. nil = it never has.
	Released func() bool
	// Found reports whether the connection attempt under way has seen the
	// speakers. nil = it never has.
	Found func() bool
	// Note tells the user, in a few words, what is being done: one note when
	// a round of requests starts, one with its outcome. nil = nobody.
	Note func(text string)
	// IsSelf reports whether peer is this very instance, which must never be
	// asked. nil = nothing is.
	IsSelf func(peer string) bool
	// Post sends the disconnect request and describes the answer. nil = HTTP.
	// It is called for several peers at the same time.
	Post func(ctx context.Context, peer string) (answer string, err error)
	Log  func(level logx.Level, format string, args ...any)
	// Timeout, FoundTimeout and PeerTimeout default to SearchTimeout,
	// ConnectTimeout and PeerTimeout.
	Timeout      time.Duration
	FoundTimeout time.Duration
	PeerTimeout  time.Duration

	searching bool
	since     time.Time       // start of the current search
	asked     map[string]bool // peers dealt with in this session; nil = none yet
}

func (t *Takeover) logf(level logx.Level, format string, args ...any) {
	if t.Log != nil {
		t.Log(level, format, args...)
	}
}

// Check looks at the state at time now and asks every peer not asked before:
// at once in a new session, otherwise when the search has lasted the whole
// timeout. Run calls it regularly; it must not be called concurrently.
func (t *Takeover) Check(ctx context.Context, now time.Time) {
	if t.Released != nil && t.Released() {
		t.searching, t.asked = false, nil
		return
	}
	if t.Connected() {
		t.searching = false
		return
	}
	if !t.searching {
		t.searching, t.since = true, now
	}
	if t.asked != nil && now.Sub(t.since) < t.timeout() {
		return
	}
	if ctx.Err() != nil {
		return
	}
	first := t.asked == nil
	if first {
		t.asked = map[string]bool{}
	}
	var round []string
	for _, peer := range t.Peers() {
		if t.asked[peer] {
			continue
		}
		t.asked[peer] = true
		round = append(round, peer)
	}
	if len(round) > 0 {
		t.askAll(ctx, round, first)
	}
}

func (t *Takeover) timeout() time.Duration {
	if t.Found != nil && t.Found() {
		if t.FoundTimeout > 0 {
			return t.FoundTimeout
		}
		return ConnectTimeout
	}
	if t.Timeout > 0 {
		return t.Timeout
	}
	return SearchTimeout
}

// Outcomes of asking one peer.
const (
	outReleased = iota // let go of the speakers
	outRefused         // answered, but did not let go
	outSilent          // did not answer: off, or not running the app
)

// askAll asks peers at the same time and waits for every answer, which
// PeerTimeout bounds. The user gets one note now and one with the outcome.
func (t *Takeover) askAll(ctx context.Context, peers []string, first bool) {
	others := t.others(peers)
	if len(others) == 0 {
		return
	}
	if len(others) == 1 {
		t.note("Asking %s to release the speakers", others[0])
	} else {
		t.note("Asking %d other devices to release the speakers", len(others))
	}
	why := "speakers not found"
	if first {
		why = "new session"
	}
	outcome := make([]int, len(others))
	var wg sync.WaitGroup
	var mu sync.Mutex // Log may not be safe for concurrent use
	for i, peer := range others {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, answer, err := t.ask(ctx, peer)
			outcome[i] = out
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// Normal: that computer is off, or the app is not running there.
				t.logf(logx.Info, "Other devices: %s did not answer (%v)", peer, err)
				return
			}
			t.logf(logx.Info, "Asked %s to release the speakers (%s): %s", peer, why, answer)
		}()
	}
	wg.Wait()
	if ctx.Err() == nil {
		t.note("%s", outcomeText(others, outcome))
	}
}

// others drops this very instance from peers. The checks may resolve names,
// so they run at the same time too.
func (t *Takeover) others(peers []string) []string {
	if t.IsSelf == nil {
		return peers
	}
	self := make([]bool, len(peers))
	var wg sync.WaitGroup
	for i, peer := range peers {
		wg.Add(1)
		go func() { defer wg.Done(); self[i] = t.IsSelf(peer) }()
	}
	wg.Wait()
	var out []string
	for i, peer := range peers {
		if self[i] {
			t.logf(logx.Info, "Other devices: %s is this computer, skipping it", peer)
		} else {
			out = append(out, peer)
		}
	}
	return out
}

func (t *Takeover) ask(ctx context.Context, peer string) (int, string, error) {
	limit := t.PeerTimeout
	if limit <= 0 {
		limit = PeerTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	post := t.Post
	if post == nil {
		post = PostDisconnect
	}
	answer, err := post(ctx, peer)
	switch {
	case err != nil:
		return outSilent, "", err
	case strings.HasPrefix(answer, "200"):
		return outReleased, answer, nil
	default:
		return outRefused, answer, nil
	}
}

// outcomeText sums up a round in one note: who released the speakers, else
// who would not, else who did not answer.
func outcomeText(peers []string, outcome []int) string {
	by := map[int][]string{}
	for i, p := range peers {
		by[outcome[i]] = append(by[outcome[i]], p)
	}
	switch {
	case len(by[outReleased]) > 0:
		return joinNames(by[outReleased]) + " released the speakers"
	case len(by[outRefused]) > 0:
		return joinNames(by[outRefused]) + " did not release the speakers"
	case len(peers) == 1:
		return peers[0] + " did not answer"
	default:
		return "No other device answered"
	}
}

// joinNames gives "a", "a and b", "a, b and c".
func joinNames(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func (t *Takeover) note(format string, args ...any) {
	if t.Note != nil {
		t.Note(fmt.Sprintf(format, args...))
	}
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

// PostDisconnect sends POST /api/disconnect to peer and returns its answer, e.g.
// `200 {"success":true}`.
func PostDisconnect(ctx context.Context, peer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+peer+"/api/disconnect", nil)
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
