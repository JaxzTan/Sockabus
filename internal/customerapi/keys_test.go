package customerapi

import (
	"strings"
	"testing"
)

func TestNewKey(t *testing.T) {
	raw, prefix, hash, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, KeyPrefix) || len(raw) != len(KeyPrefix)+48 || !LooksLikeKey(raw) {
		t.Fatalf("bad key %q", raw)
	}
	if prefix != raw[:12] || len(prefix) > 16 {
		t.Fatalf("prefix %q", prefix)
	}
	if hash != Hash(raw) || !strings.HasPrefix(hash, "sha256:") || len(hash) != 7+64 || strings.Contains(hash, raw) {
		t.Fatalf("hash %q", hash)
	}
	raw2, _, hash2, _ := NewKey()
	if raw2 == raw || hash2 == hash {
		t.Fatal("keys must differ")
	}
	if LooksLikeKey("operator-token-abc") {
		t.Fatal("operator token is not a customer key")
	}
}
