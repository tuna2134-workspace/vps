package repositories

import (
	"net/netip"
	"testing"
)

func TestPrefixAt(t *testing.T) {
	pool := netip.MustParsePrefix("2001:db8:100::/48")

	cases := []struct {
		index uint64
		want  string
	}{
		{0, "2001:db8:100::/64"},
		{1, "2001:db8:100:1::/64"},
		{2, "2001:db8:100:2::/64"},
		{255, "2001:db8:100:ff::/64"},
		{256, "2001:db8:100:100::/64"},
	}
	for _, c := range cases {
		got, err := PrefixAt(pool, 64, c.index)
		if err != nil {
			t.Fatalf("PrefixAt(%d): %v", c.index, err)
		}
		if got.String() != c.want {
			t.Errorf("PrefixAt(%d) = %s, want %s", c.index, got, c.want)
		}
	}
}

func TestPrefixAtRejectsInvalidDelegation(t *testing.T) {
	pool := netip.MustParsePrefix("2001:db8:100::/48")
	if _, err := PrefixAt(pool, 48, 0); err == nil {
		t.Error("delegation length equal to pool length must be rejected")
	}
	if _, err := PrefixAt(pool, 128, 0); err == nil {
		t.Error("delegation length 128 must be rejected (no host bits)")
	}
	if _, err := PrefixAt(netip.MustParsePrefix("192.0.2.0/24"), 64, 0); err == nil {
		t.Error("IPv4 prefix delegation must be rejected")
	}
}

func TestPrefixAtIndexOutOfRange(t *testing.T) {
	pool := netip.MustParsePrefix("2001:db8:100::/48")
	if _, err := PrefixAt(pool, 56, 1<<8); err == nil {
		t.Error("index beyond 2^8 must be rejected for /56 delegation")
	}
}
