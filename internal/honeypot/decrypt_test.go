package honeypot

import (
	"bytes"
	"encoding/base64"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JaxzTan/Buru_AI/internal/db"
	"github.com/JaxzTan/Buru_AI/internal/msgcrypt"
)

func testService(t *testing.T) *Service {
	t.Helper()
	box, err := msgcrypt.New(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Box: box, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// A corrupted message decrypts to the flagged placeholder (with the error), and readable() keeps it
// away from every LLM prompt.
func TestDecryptPlaceholderAndReadable(t *testing.T) {
	s := testService(t)
	ct, ref, err := s.Box.Seal([]byte("transfer to Maybank 1642 0457 1893"), aad(7))
	if err != nil {
		t.Fatal(err)
	}
	good := db.Message{ID: 1, ConversationID: 7, Direction: db.MessageDirectionInbound, SenderType: db.SenderTypeScammer, BodyCiphertext: ct, DekRef: ref, SentAt: time.Now()}
	bad := good
	bad.ID = 2
	bad.BodyCiphertext = append([]byte(nil), ct...)
	bad.BodyCiphertext[len(ct)-1] ^= 1

	pm, err := s.decrypt(good)
	if err != nil || pm.Undecryptable || pm.Text != "transfer to Maybank 1642 0457 1893" {
		t.Fatalf("good message = %+v, %v", pm, err)
	}
	pb, err := s.decrypt(bad)
	if err == nil || !pb.Undecryptable || pb.Text != UndecryptableText || pb.ID != 2 || pb.SenderType != "scammer" {
		t.Fatalf("bad message = %+v, %v; want flagged placeholder and an error", pb, err)
	}
	if got := readable([]PlainMessage{pm, pb}); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("readable = %+v; want only the good message", got)
	}
}

// Identifiers: sealed values win over plaintext; a broken ciphertext falls back to the plaintext; a
// broken ciphertext with no plaintext reports not-ok so extraction skips it.
func TestRevealIdentifiers(t *testing.T) {
	s := testService(t)
	sealed, err := s.sealConversationIdentifiers(5, "@scam", "tg:user:1:2")
	if err != nil {
		t.Fatal(err)
	}
	var h, p pgtype.Text
	if !s.revealIdentifiers(1, 5, sealed.handle, sealed.peerRef, sealed.dekRef, &h, &p) || h.String != "@scam" || p.String != "tg:user:1:2" {
		t.Fatalf("reveal = %v %v", h, p)
	}
	// Wrong account (AAD) = cannot open: plaintext copy kept.
	h, p = pgtype.Text{String: "@plain", Valid: true}, pgtype.Text{String: "tg:user:1:2", Valid: true}
	if !s.revealIdentifiers(1, 6, sealed.handle, sealed.peerRef, sealed.dekRef, &h, &p) || h.String != "@plain" {
		t.Fatalf("fallback reveal = %v", h)
	}
	h, p = pgtype.Text{}, pgtype.Text{}
	if s.revealIdentifiers(1, 6, sealed.handle, sealed.peerRef, sealed.dekRef, &h, &p) || p.Valid {
		t.Fatal("undecryptable identifiers with no plaintext must report not ok")
	}
	if !bytes.Equal(sealed.peerBidx, s.Box.PeerRefIndex("tg:user:1:2")) {
		t.Fatal("blind index differs from PeerRefIndex")
	}
}
