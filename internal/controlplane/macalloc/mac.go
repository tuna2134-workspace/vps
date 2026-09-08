// Package macalloc generates unique, locally-administered MAC addresses.
package macalloc

import (
	"crypto/rand"
	"context"
	"errors"
	"fmt"

	"github.com/tuna2134/vps/internal/controlplane/repositories"
)

var ErrExhausted = errors.New("unable to allocate a unique MAC address")

// Generator produces unique MAC addresses by retrying random draws against
// the database uniqueness constraint.
type Generator struct {
	allocator macAllocator
}

func NewGenerator(vms *repositories.VMRepository) *Generator {
	return &Generator{allocator: vms}
}

type macAllocator interface {
	Exists(ctx context.Context, mac string) (bool, error)
}

// Generate returns a unique locally-administered unicast MAC address.
// The locally administered bit (second least significant bit of the first
// octet) is set, and the multicast bit is cleared.
func (g *Generator) Generate(ctx context.Context, allocator macAllocator) (string, error) {
	if allocator != nil {
		g.allocator = allocator
	}
	if g.allocator == nil {
		return "", errors.New("no mac allocator configured")
	}
	for i := 0; i < 16; i++ {
		mac, err := randomMAC()
		if err != nil {
			return "", err
		}
		exists, err := g.allocator.Exists(ctx, mac)
		if err != nil {
			return "", fmt.Errorf("check mac availability: %w", err)
		}
		if !exists {
			return mac, nil
		}
	}
	return "", ErrExhausted
}

func randomMAC() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random mac: %w", err)
	}
	// Set locally administered bit (0x02) and clear multicast bit (0x01).
	buf[0] = (buf[0] | 0x02) &^ 0x01
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		buf[0], buf[1], buf[2], buf[3], buf[4], buf[5]), nil
}

// RandomMAC exposes a raw random locally-administered MAC (used by tests and
// the agent where DB uniqueness is enforced by the control plane).
func RandomMAC() (string, error) {
	return randomMAC()
}