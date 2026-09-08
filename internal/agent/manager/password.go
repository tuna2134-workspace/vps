package manager

import (
	"crypto/rand"
	"math/big"
)

const passwordChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// vncPasswordLength is the maximum password length QEMU's VNC server accepts.
const vncPasswordLength = 8

// randomPassword returns a random alphanumeric password for VNC access.
// QEMU limits VNC passwords to 8 characters.
func randomPassword() string {
	b := make([]byte, vncPasswordLength)
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
