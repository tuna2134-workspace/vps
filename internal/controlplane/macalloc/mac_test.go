package macalloc

import (
	"context"
	"strings"
	"testing"
)

type fakeAllocator struct {
	used map[string]bool
}

func (f *fakeAllocator) Exists(ctx context.Context, mac string) (bool, error) {
	return f.used[mac], nil
}

func TestRandomMACIsLocallyAdministered(t *testing.T) {
	mac, err := RandomMAC()
	if err != nil {
		t.Fatalf("RandomMAC: %v", err)
	}
	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		t.Fatalf("expected 6 octets, got %q", mac)
	}
	first, err := hexToByte(parts[0])
	if err != nil {
		t.Fatalf("bad first octet %q: %v", parts[0], err)
	}
	// Locally administered bit (0x02) set, multicast bit (0x01) clear.
	if first&0x02 == 0 {
		t.Errorf("locally administered bit not set: %s", mac)
	}
	if first&0x01 != 0 {
		t.Errorf("multicast bit must be clear: %s", mac)
	}
}

func TestGeneratorAvoidsUsed(t *testing.T) {
	g := NewGenerator(nil)
	fake := &fakeAllocator{used: map[string]bool{}}
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		mac, err := g.Generate(context.Background(), fake)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if seen[mac] {
			t.Fatalf("duplicate mac generated: %s", mac)
		}
		seen[mac] = true
		fake.used[mac] = true
	}
}

func TestGeneratorExhaustion(t *testing.T) {
	g := NewGenerator(nil)
	// Pre-mark every possible draw as used: use a blocklist that matches
	// anything so the generator can never find a free address.
	fake := &blockAllocator{}
	_, err := g.Generate(context.Background(), fake)
	if err != ErrExhausted {
		t.Errorf("expected ErrExhausted, got %v", err)
	}
}

type blockAllocator struct{}

func (b *blockAllocator) Exists(ctx context.Context, mac string) (bool, error) {
	return true, nil
}

func hexToByte(s string) (byte, error) {
	var v byte
	for _, r := range s {
		if r >= '0' && r <= '9' {
			v = v<<4 | byte(r-'0')
		} else if r >= 'a' && r <= 'f' {
			v = v<<4 | byte(r-'a'+10)
		} else {
			return 0, errBadHex
		}
	}
	return v, nil
}

var errBadHex = &hexError{}

type hexError struct{}

func (*hexError) Error() string { return "bad hex" }
