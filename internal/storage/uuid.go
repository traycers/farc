package storage

import (
	"crypto/rand"
	"fmt"
)

// newUUIDv4 generates a random UUIDv4 (docs/docs/archive/00-requirements.md:
// every fcontainer is identified by a logical UUIDv4, in addition to its
// physical index).
func newUUIDv4() ([16]byte, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return u, fmt.Errorf("storage: generate UUIDv4: %w", err)
	}
	u[6] = (u[6] & 0x0f) | 0x40 // version 4
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 4122 variant
	return u, nil
}
