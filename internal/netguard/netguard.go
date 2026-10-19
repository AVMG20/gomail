// Package netguard builds HTTP transports for requests to URLs taken from
// untrusted mail (one-click unsubscribe): they refuse loopback, private,
// link-local and other non-public addresses so a message can't make gomail
// talk to the local network (SSRF), including via DNS rebinding.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
	"syscall"
	"time"
)

// ErrBlocked is returned for non-public destinations.
var ErrBlocked = errors.New("refusing to connect to a local or private network address")

// IsPublic reports whether ip is a globally routable unicast address.
func IsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64 (could map to private v4)
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
}

// Resolver is used to check hosts reached through a proxy (tests replace it).
var Resolver = net.DefaultResolver

// Transport returns an http.Transport that only connects to public
// addresses. Direct connections are checked at dial time (after DNS, so
// rebinding can't sneak past). Through a proxy the target host is resolved
// and checked first, and the proxy itself is allowed.
func Transport() *http.Transport {
	var mu sync.Mutex
	proxies := map[string]bool{} // proxy "ip:port" we may dial
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = func(req *http.Request) (*url.URL, error) {
		pu, err := http.ProxyFromEnvironment(req)
		if err != nil || pu == nil {
			return pu, err
		}
		if err := checkHost(req.Context(), req.URL.Hostname()); err != nil {
			return nil, err
		}
		port := pu.Port()
		if port == "" {
			port = map[string]string{"https": "443"}[pu.Scheme]
			if port == "" {
				port = "80"
			}
		}
		addrs, err := Resolver.LookupNetIP(req.Context(), "ip", pu.Hostname())
		if err != nil {
			return nil, err
		}
		mu.Lock()
		for _, a := range addrs {
			proxies[netip.AddrPortFrom(a.Unmap(), parsePort(port)).String()] = true
		}
		mu.Unlock()
		return pu, nil
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return ErrBlocked
			}
			mu.Lock()
			isProxy := proxies[netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()).String()]
			mu.Unlock()
			if !isProxy && !IsPublic(ap.Addr()) {
				return fmt.Errorf("%w (%s)", ErrBlocked, ap.Addr())
			}
			return nil
		}}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		// Literal addresses are refused before any socket is made; names are
		// checked by Control after resolution.
		if ap, err := netip.ParseAddrPort(address); err == nil {
			mu.Lock()
			isProxy := proxies[netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()).String()]
			mu.Unlock()
			if !isProxy && !IsPublic(ap.Addr()) {
				return nil, fmt.Errorf("%w (%s)", ErrBlocked, ap.Addr())
			}
		}
		return d.DialContext(ctx, network, address)
	}
	return t
}

func parsePort(s string) uint16 {
	var n uint16
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + uint16(c-'0')
	}
	return n
}

// checkHost resolves host and fails if any address is not public.
func checkHost(ctx context.Context, host string) error {
	if ip, err := netip.ParseAddr(host); err == nil {
		if !IsPublic(ip) {
			return fmt.Errorf("%w (%s)", ErrBlocked, ip)
		}
		return nil
	}
	addrs, err := Resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return err
	}
	for _, a := range addrs {
		if !IsPublic(a) {
			return fmt.Errorf("%w (%s resolves to %s)", ErrBlocked, host, a)
		}
	}
	return nil
}
