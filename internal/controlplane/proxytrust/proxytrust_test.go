package proxytrust

import (
	"net/http"
	"net/netip"
	"testing"
)

func mustPT(t *testing.T, cidrs ...string) *ProxyTrust {
	t.Helper()
	pt, err := New(cidrs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return pt
}

func req(remote, xff string) *http.Request {
	return &http.Request{
		RemoteAddr: remote,
		Header:     http.Header{"X-Forwarded-For": []string{xff}},
	}
}

// TestTrustedProxyWithXFF verifies a trusted proxy's XFF is honored.
func TestTrustedProxyWithXFF(t *testing.T) {
	pt := mustPT(t, "127.0.0.1/32", "10.0.0.0/8")
	got := pt.ClientIP(req("127.0.0.1:54321", "203.0.113.9"))
	want := netip.MustParseAddr("203.0.113.9")
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestUntrustedClientSpoofedXFF verifies a direct (untrusted) client cannot
// spoof its IP via X-Forwarded-For.
func TestUntrustedClientSpoofedXFF(t *testing.T) {
	pt := mustPT(t, "127.0.0.1/32")
	got := pt.ClientIP(req("203.0.113.7:1234", "127.0.0.1"))
	want := netip.MustParseAddr("203.0.113.7")
	if got != want {
		t.Fatalf("spoofed XFF was trusted: got %v, want %v", got, want)
	}
}

// TestTrustedProxyChain verifies the right-most untrusted hop is returned.
func TestTrustedProxyChain(t *testing.T) {
	pt := mustPT(t, "127.0.0.1/32", "10.0.0.0/8", "172.16.0.0/12")
	// client 198.51.100.4 -> proxy 10.0.0.2 -> proxy 172.16.0.3 -> us
	got := pt.ClientIP(req("172.16.0.3:443", "198.51.100.4, 10.0.0.2, 172.16.0.3"))
	want := netip.MustParseAddr("198.51.100.4")
	if got != want {
		t.Fatalf("chain resolution: got %v, want %v", got, want)
	}
}

// TestNoXFF verifies the remote address is used when no header exists.
func TestNoXFF(t *testing.T) {
	pt := mustPT(t, "127.0.0.1/32")
	got := pt.ClientIP(req("127.0.0.1:8080", ""))
	want := netip.MustParseAddr("127.0.0.1")
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestMalformedXFF verifies malformed entries are skipped safely.
func TestMalformedXFF(t *testing.T) {
	pt := mustPT(t, "127.0.0.1/32")
	got := pt.ClientIP(req("127.0.0.1:8080", "not-an-ip, 203.0.113.5, garbage"))
	want := netip.MustParseAddr("203.0.113.5")
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestIPv6TrustedProxy verifies IPv6 forwarding works.
func TestIPv6TrustedProxy(t *testing.T) {
	pt := mustPT(t, "::1/128", "2001:db8::/32")
	got := pt.ClientIP(req("[::1]:8080", "2001:db8::5"))
	want := netip.MustParseAddr("2001:db8::5")
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
