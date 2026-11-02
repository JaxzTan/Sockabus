package msgcrypt

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, ref, err := b.Seal([]byte("send to Maybank 1642 0457 1893"), []byte("conv:1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("Maybank")) {
		t.Fatal("ciphertext contains plaintext")
	}
	pt, err := b.Open(ct, ref, []byte("conv:1"))
	if err != nil || string(pt) != "send to Maybank 1642 0457 1893" {
		t.Fatalf("open = %q, %v", pt, err)
	}
	if _, err := b.Open(ct, ref, []byte("conv:2")); err == nil {
		t.Fatal("open with wrong aad should fail")
	}
}

func TestBadKeys(t *testing.T) {
	for _, k := range []string{"", "not-base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := New(k); err == nil {
			t.Errorf("New(%q) should fail", k)
		}
	}
}

func TestBlindIndex(t *testing.T) {
	k1 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	k2 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	b1, _ := New(k1)
	b1again, _ := New(k1)
	b2, _ := New(k2)
	v := []byte("tg:user:12345:987654321")
	i1 := b1.BlindIndex("peer_ref", v)
	if len(i1) != 32 {
		t.Fatalf("len = %d, want 32", len(i1))
	}
	if !bytes.Equal(i1, b1again.BlindIndex("peer_ref", v)) {
		t.Fatal("blind index must be deterministic for the same key")
	}
	if bytes.Equal(i1, b2.BlindIndex("peer_ref", v)) {
		t.Fatal("different keys must give different indexes")
	}
	if bytes.Equal(i1, b1.BlindIndex("handle", v)) {
		t.Fatal("different domains must give different indexes")
	}
	if bytes.Equal(i1, b1.BlindIndex("peer_ref", []byte("tg:user:12345:987654322"))) {
		t.Fatal("different values must give different indexes")
	}
}
