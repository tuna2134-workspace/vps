package manager

import (
	"crypto/rand"
	"math/big"
)

const passwordChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// randomPassword returns a random alphanumeric password for VNC access.
func randomPassword() string {
	b := make([]byte, 24)
	max := big.NewInt(int64(len(passwordChars)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "changeme"
		}
		b[i] = passwordChars[n.Int64()]
	}
	return string(b)
}