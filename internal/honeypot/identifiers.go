package honeypot

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JaxzTan/Buru_AI/internal/db"
	"github.com/JaxzTan/Buru_AI/internal/msgcrypt"
)

// Scammer identifiers at rest (TRD §7, migration 000012). conversations.scammer_handle /
// scammer_peer_ref and personas.demo_identifiers have sealed copies; routing matches the keyed blind
// index. Phase 1: the plaintext columns are still written (Config.PlaintextIdentifiers, default on) and
// read as a fallback for rows cmd/backfill-encrypt has not reached. Phase 2
// (db/migrations-pending/*_drop_plaintext_identifiers) clears the plaintext; the app then reads only
// the ciphertext. Responses keep their JSON shape: the decrypted values go into the plaintext fields.

type sealedIdentifiers struct {
	handle, peerRef, peerBidx []byte
	dekRef                    pgtype.Text
}

func (s *Service) sealConversationIdentifiers(accountID int64, handle, peerRef string) (sealedIdentifiers, error) {
	var out sealedIdentifiers
	ct, ref, err := s.Box.Seal([]byte(handle), msgcrypt.AADConvHandle(accountID))
	if err != nil {
		return out, err
	}
	out.handle = ct
	if peerRef != "" {
		if out.peerRef, _, err = s.Box.Seal([]byte(peerRef), msgcrypt.AADConvPeerRef(accountID)); err != nil {
			return out, err
		}
		out.peerBidx = s.Box.PeerRefIndex(peerRef)
	}
	out.dekRef = pgtype.Text{String: ref, Valid: true}
	return out, nil
}

// plaintextOrNull is what goes into a plaintext identifier column: the value in phase 1, NULL in phase 2.
func (s *Service) plaintextOrNull(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: s.Cfg.PlaintextIdentifiers && v != ""}
}

func (s *Service) plaintextJSONOrNull(v json.RawMessage) json.RawMessage {
	if !s.Cfg.PlaintextIdentifiers {
		return nil
	}
	return v
}

// revealIdentifiers replaces handle/peerRef with the decrypted ciphertext when there is one. A sealed
// value that fails to open is logged and the plaintext copy (if any) is kept; ok is false when the
// handle could not be recovered at all, so callers do not treat it as intelligence.
func (s *Service) revealIdentifiers(convID, accountID int64, handleCT, peerCT []byte, dekRef pgtype.Text, handle, peerRef *pgtype.Text) bool {
	if len(handleCT) > 0 {
		if pt, err := s.Box.Open(handleCT, dekRef.String, msgcrypt.AADConvHandle(accountID)); err == nil {
			*handle = pgtype.Text{String: string(pt), Valid: true}
		} else {
			s.Log.Error("scammer handle could not be decrypted", "conversation_id", convID, "err", err)
		}
	}
	if len(peerCT) > 0 {
		if pt, err := s.Box.Open(peerCT, dekRef.String, msgcrypt.AADConvPeerRef(accountID)); err == nil {
			*peerRef = pgtype.Text{String: string(pt), Valid: true}
		} else {
			s.Log.Error("scammer peer ref could not be decrypted", "conversation_id", convID, "err", err)
		}
	}
	return handle.Valid
}

func (s *Service) revealConversation(c *db.Conversation) bool {
	return s.revealIdentifiers(c.ID, c.AccountID, c.ScammerHandleCiphertext, c.ScammerPeerRefCiphertext, c.IdentifiersDekRef, &c.ScammerHandle, &c.ScammerPeerRef)
}

// conversation loads a conversation with its identifiers decrypted.
func (s *Service) conversation(ctx context.Context, id int64) (db.Conversation, error) {
	c, err := s.Q.GetConversation(ctx, id)
	if err != nil {
		return c, err
	}
	s.revealConversation(&c)
	return c, nil
}

// ListConversations is the operator console list (GET /internal/conversations), identifiers decrypted.
func (s *Service) ListConversations(ctx context.Context, p db.ListConversationsWithPersonaParams) ([]db.ListConversationsWithPersonaRow, error) {
	rows, err := s.Q.ListConversationsWithPersona(ctx, p)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		r := &rows[i]
		s.revealIdentifiers(r.ID, r.AccountID, r.ScammerHandleCiphertext, r.ScammerPeerRefCiphertext, r.IdentifiersDekRef, &r.ScammerHandle, &r.ScammerPeerRef)
	}
	return rows, nil
}

// revealPersona puts the decrypted demo identifiers into p.DemoIdentifiers.
func (s *Service) revealPersona(p *db.Persona) {
	if len(p.DemoIdentifiersCiphertext) == 0 {
		return
	}
	pt, err := s.Box.Open(p.DemoIdentifiersCiphertext, p.DemoIdentifiersDekRef.String, msgcrypt.AADPersonaDemoIDs(p.ID))
	if err != nil || !json.Valid(pt) {
		s.Log.Error("persona demo identifiers could not be decrypted", "persona_id", p.ID, "err", err)
		return // keep the plaintext copy, if any
	}
	p.DemoIdentifiers = json.RawMessage(pt)
}

// sealPersonaDemoIDs stores the sealed demo identifiers of a just-created persona (same transaction).
func (s *Service) sealPersonaDemoIDs(ctx context.Context, q *db.Queries, personaID int64, ids json.RawMessage) error {
	if len(ids) == 0 {
		return nil
	}
	ct, ref, err := s.Box.Seal(ids, msgcrypt.AADPersonaDemoIDs(personaID))
	if err != nil {
		return err
	}
	_, err = q.SetPersonaDemoIdentifiersCiphertext(ctx, db.SetPersonaDemoIdentifiersCiphertextParams{ID: personaID,
		DemoIdentifiersCiphertext: ct, DemoIdentifiersDekRef: pgtype.Text{String: ref, Valid: true}})
	return err
}
