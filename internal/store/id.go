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
	return randomID("job")
}

// newUsageEntryID generates a random usage ledger entry identifier, same
// scheme as newJobID.
func newUsageEntryID() (string, error) {
	return randomID("usage")
}

func randomID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(b), nil
}
