package customerapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Customer API keys (TR-13, TRD G-7). Format: "sk_live_" + 48 hex chars (24 random bytes,
// 192 bits). Only the hash is stored (api_keys.key_hash, UNIQUE); the raw key is shown once.
//
// Hash scheme: "sha256:" + hex(SHA-256(raw key)). A plain fast hash is the standard choice for
// high-entropy random keys (nothing to brute-force, unlike passwords) and lets the lookup API find
// the key with one indexed equality query. key_prefix (first 12 chars, e.g. "sk_live_3fa9") is
// stored in clear so operators can tell keys apart.
const (
	KeyPrefix    = "sk_live_"
	prefixLen    = 12
	randomBytes  = 24
	hashScheme   = "sha256:"
	maxKeyLength = 128
)

// NewKey returns a fresh raw key, its display prefix and its stored hash.
func NewKey() (raw, prefix, hash string, err error) {
	b := make([]byte, randomBytes)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	raw = KeyPrefix + hex.EncodeToString(b)
	return raw, raw[:prefixLen], Hash(raw), nil
}

// Hash is the value stored in api_keys.key_hash for a raw key.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hashScheme + hex.EncodeToString(sum[:])
}

// LooksLikeKey reports whether a bearer token has the customer-key shape (cheap pre-check before
// the database lookup).
func LooksLikeKey(token string) bool {
	return strings.HasPrefix(token, "sk_") && len(token) > prefixLen && len(token) <= maxKeyLength
}
