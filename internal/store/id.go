package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newJobID generates a random, sufficiently unique job identifier without
// pulling in a UUID dependency for something this small: 16 bytes of
// crypto/rand hex-encoded, prefixed for readability in logs and URLs.
func newJobID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating job id: %w", err)
	}
	return "job_" + hex.EncodeToString(b), nil
}
