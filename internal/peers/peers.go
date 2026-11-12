// Package peers handles the "other computers" of an installation: machines
// that may run this app too. The speakers accept one Bluetooth LE controller
// at a time, so an instance that cannot find them asks its peers to
// disconnect (POST /api/disconnect) and thereby release the link. The newest
// instance wins.
package peers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// DefaultPort is the port the app listens on unless told otherwise, and so
// the port assumed for a peer given without one.
const DefaultPort = 5000

// MaxPeers bounds the list; a household has a handful of computers.
const MaxPeers = 16

// ErrFixed is returned by Save while the list is fixed by the --peers flag.
var ErrFixed = errors.New("the list was set with --peers for this run and cannot be changed here")

// Normalize validates one entry, "host" or "host:port", and returns it as
// "host:port" with the default port filled in.
func Normalize(entry string, defaultPort int) (string, error) {
	entry = strings.TrimSpace(entry)
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("%q is not a valid address (%s). Use a name or IP address, optionally with a port: living-room-pc or 192.168.1.20:5000", entry, why)
	}
	if entry == "" {
		return bad("it is empty")
	}
	if strings.Contains(entry, "://") || strings.ContainsAny(entry, "/?#@ ") {
		return bad("leave out http:// and any path")
	}
	host, port := entry, strconv.Itoa(defaultPort)
	switch {
	case strings.HasPrefix(entry, "["): // [IPv6] or [IPv6]:port
		if strings.HasSuffix(entry, "]") {
			host = entry[1 : len(entry)-1]
		} else {
			h, p, err := net.SplitHostPort(entry)
			if err != nil {
				return bad("the brackets or the port are wrong")
			}
			host, port = h, p
		}
		if ip := net.ParseIP(host); ip == nil || ip.To4() != nil {
			return bad("the brackets must hold an IPv6 address")
		}
	case strings.Count(entry, ":") == 1:
		h, p, err := net.SplitHostPort(entry)
		if err != nil {
			return bad("the port is wrong")
		}
		host, port = h, p
	case strings.Count(entry, ":") > 1: // bare IPv6
		if net.ParseIP(entry) == nil {
			return bad("write IPv6 addresses as [address]:port")
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return bad("the port must be a number from 1 to 65535")
	}
	if net.ParseIP(host) == nil && !validHostname(host) {
		return bad("the name may only contain letters, digits, hyphens and dots")
	}
	return net.JoinHostPort(strings.ToLower(host), strconv.Itoa(n)), nil
}

func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	digitsOnly := true
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-', c == '_':
				digitsOnly = false
			default:
				return false
			}
		}
	}
	return !digitsOnly // "1.2.3" or "300.1.1.1" is a mistyped IP address, not a name
}

// ParseList validates a comma-separated list. Blank items are skipped and
// duplicates dropped, so an empty string gives an empty list.
func ParseList(list string, defaultPort int) ([]string, error) {
	out := []string{}
	for _, item := range strings.Split(list, ",") {
		if strings.TrimSpace(item) == "" {
			continue
		}
		p, err := Normalize(item, defaultPort)
		if err != nil {
			return nil, err
		}
		dup := false
		for _, have := range out {
			dup = dup || have == p
		}
		if !dup {
			out = append(out, p)
		}
	}
	if len(out) > MaxPeers {
		return nil, fmt.Errorf("too many addresses: at most %d", MaxPeers)
	}
	return out, nil
}

// config is the JSON document in the config file.
type config struct {
	Peers []string `json:"peers"`
}

// Store holds the peer list: the one saved in the config file, or the one
// from the --peers flag that replaces it for a run. It is safe for
// concurrent use.
type Store struct {
	path        string // "" = not persisted
	defaultPort int

	mu    sync.Mutex
	saved []string
	fixed []string // non-nil: set by the flag
}

// Open reads the list saved in path; a missing file is an empty list. With
// an empty path the Store only lives in memory. On a read or parse error the
// returned Store is still usable and starts empty.
func Open(path string, defaultPort int) (*Store, error) {
	s := &Store{path: path, defaultPort: defaultPort, saved: []string{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	list, err := ParseList(strings.Join(c.Peers, ","), defaultPort)
	if err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	s.saved = list
	return s, nil
}

// Fix replaces the list for this run (the --peers flag). The saved list is
// kept in the file and can no longer be edited until the next run.
func (s *Store) Fix(list string) error {
	l, err := ParseList(list, s.defaultPort)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.fixed = l
	s.mu.Unlock()
	return nil
}

// List returns the peers in effect, and whether they are fixed by the flag.
func (s *Store) List() (list []string, fixed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fixed != nil {
		return append([]string{}, s.fixed...), true
	}
	return append([]string{}, s.saved...), false
}

// Save validates a comma-separated list, writes it to the config file and
// puts it into effect at once. It returns the list as stored.
func (s *Store) Save(list string) ([]string, error) {
	l, err := ParseList(list, s.defaultPort)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fixed != nil {
		return nil, ErrFixed
	}
	if s.path != "" {
		if err := writeFile(s.path, config{Peers: l}); err != nil {
			return nil, fmt.Errorf("could not save: %w", err)
		}
	}
	s.saved = l
	return append([]string{}, l...), nil
}

// writeFile replaces path atomically, so a crash cannot leave half a file.
func writeFile(path string, c config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
