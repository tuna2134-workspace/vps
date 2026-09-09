// Package proxytrust safely resolves a client IP from an http.Request, only
// trusting forwarded headers when the direct peer is a known reverse proxy.
package proxytrust

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ProxyTrust decides which IP is the real client behind optional reverse
// proxies. Forwarded headers (X-Forwarded-For) are only honored when the
// connection's RemoteAddr is inside a trusted CIDR.
type ProxyTrust struct {
	networks []netip.Prefix
}

// New builds a ProxyTrust from a list of CIDRs (e.g. "127.0.0.1/32").
func New(cidrs []string) (*ProxyTrust, error) {
	pt := &ProxyTrust{}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, err
		}
		pt.networks = append(pt.networks, p.Masked())
	}
	return pt, nil
}

// IsTrusted reports whether addr is a trusted proxy.
func (p *ProxyTrust) IsTrusted(addr netip.Addr) bool {
	for _, n := range p.networks {
		if n.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP returns the real client IP. When the direct peer is trusted, the
// right-most untrusted entry in X-Forwarded-For is used; otherwise the remote
// address is returned and spoofed forwarded headers are ignored.
func (p *ProxyTrust) ClientIP(r *http.Request) netip.Addr {
	remote := parseRemoteAddr(r.RemoteAddr)
	if !remote.IsValid() || !p.IsTrusted(remote) {
		return remote
	}

	// Walk X-Forwarded-For from right to left, keeping entries added by
	// trusted proxies and stopping at the first untrusted hop (the client).
	entries := parseForwardedFor(r.Header.Get("X-Forwarded-For"))
	client := remote
	for i := len(entries) - 1; i >= 0; i-- {
		addr := entries[i]
		if !addr.IsValid() {
			continue
		}
		if p.IsTrusted(addr) {
			// This hop is another trusted proxy; keep looking.
			client = addr
			continue
		}
		return addr
	}
	return client
}

// parseRemoteAddr extracts the IP from a "host:port" RemoteAddr, falling back
// to the raw value when there is no port.
func parseRemoteAddr(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, _ := netip.ParseAddr(host)
	return addr
}

// parseForwardedFor splits a comma-separated X-Forwarded-For header into
// addresses, ignoring malformed entries.
func parseForwardedFor(v string) []netip.Addr {
	parts := strings.Split(v, ",")
	out := make([]netip.Addr, 0, len(parts))
	for _, p := range parts {
		if a, err := netip.ParseAddr(strings.TrimSpace(p)); err == nil {
			out = append(out, a)
		}
	}
	return out
}
