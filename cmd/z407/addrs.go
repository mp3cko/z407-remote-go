package main

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/mp3cko/z407-remote-go/internal/logx"
)

// hostAddr is a LAN address the UI can be reached on.
type hostAddr struct {
	Iface string
	IP    string
}

// ifaceAddrs is the input of pickAddrs: one network interface and its IPv4s.
type ifaceAddrs struct {
	Name string
	Up   bool
	IPs  []net.IP
}

// virtualPrefixes are interface names that never carry the user's LAN.
var virtualPrefixes = []string{"lo", "docker", "br-", "veth", "virbr", "vEthernet", "vmnet", "VMware", "VirtualBox", "Loopback", "bridge", "awdl", "llw", "utun", "gif", "stf", "anpi"}

func isVirtual(name string) bool {
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// rank orders addresses by how likely they are to be the home LAN:
// 192.168/16 first, then 172.16/12, then 10/8 (often VPNs), then the rest.
func rank(ip net.IP) int {
	switch {
	case ip[0] == 192 && ip[1] == 168:
		return 0
	case ip[0] == 172 && ip[1]&0xf0 == 16:
		return 1
	case ip[0] == 10:
		return 2
	}
	return 3
}

// pickAddrs keeps usable IPv4 addresses (up, non-loopback, non-link-local,
// non-virtual) sorted by rank. primary (the outbound-route IP) breaks ties
// within a rank. It never returns an empty list: it falls back to primary.
func pickAddrs(ifs []ifaceAddrs, primary string) []hostAddr {
	var out []hostAddr
	for _, i := range ifs {
		if !i.Up || isVirtual(i.Name) {
			continue
		}
		for _, ip := range i.IPs {
			if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() && !ip4.IsLinkLocalUnicast() {
				out = append(out, hostAddr{Iface: i.Name, IP: ip4.String()})
			}
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		ra, rb := rank(net.ParseIP(out[a].IP).To4()), rank(net.ParseIP(out[b].IP).To4())
		if ra != rb {
			return ra < rb
		}
		return out[a].IP == primary && out[b].IP != primary
	})
	if len(out) == 0 {
		out = []hostAddr{{IP: primary}}
	}
	return out
}

// systemIfaces lists the machine's interfaces.
func systemIfaces() []ifaceAddrs {
	nis, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []ifaceAddrs
	for _, ni := range nis {
		as, err := ni.Addrs()
		if err != nil {
			continue
		}
		ia := ifaceAddrs{Name: ni.Name, Up: ni.Flags&net.FlagUp != 0}
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok {
				ia.IPs = append(ia.IPs, n.IP)
			}
		}
		out = append(out, ia)
	}
	return out
}

// webURLs lists the addresses the remote is served at, best guess first:
// the bound host, or every LAN address when bound to all interfaces.
func webURLs(addrs []hostAddr, port int, host string) []string {
	if ip := net.ParseIP(host); host != "" && (ip == nil || !ip.IsUnspecified()) {
		addrs = []hostAddr{{IP: host}}
	}
	urls := make([]string, len(addrs))
	for i, a := range addrs {
		urls[i] = "http://" + net.JoinHostPort(a.IP, strconv.Itoa(port))
	}
	return urls
}

// urlLines renders one "👉 http://ip:port (iface)" line per address.
func urlLines(addrs []hostAddr, port int) string {
	var b strings.Builder
	for _, a := range addrs {
		b.WriteString(logx.Yellow(fmt.Sprintf("      👉 http://%s:%d", a.IP, port)))
		if a.Iface != "" {
			fmt.Fprintf(&b, " (%s)", a.Iface)
		}
		b.WriteString("\n")
	}
	return b.String()
}
