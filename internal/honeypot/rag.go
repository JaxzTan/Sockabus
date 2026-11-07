package honeypot

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/JaxzTan/Buru_AI/internal/db"
	"github.com/JaxzTan/Buru_AI/internal/persona"
	"github.com/JaxzTan/Buru_AI/internal/rag"
)

const (
	ragLookback = 365 * 24 * time.Hour // matches the 12-month retention purge (000013)
	ragMaxRows  = 50000
	ragExamples = 3
)

// RefreshRAG rebuilds the in-memory reply-retrieval index from conversations that extracted a
// bank account or wallet. Undecryptable messages are skipped. A no-op when s.RAG is nil.
func (s *Service) RefreshRAG(ctx context.Context) error {
	if s.RAG == nil {
		return nil
	}
	rows, err := s.Q.ListRAGMessages(ctx, db.ListRAGMessagesParams{Since: s.Now().Add(-ragLookback), RowLimit: ragMaxRows})
	if err != nil {
		return err
	}
	groups := map[int64]string{}
	msgs := make([]rag.Message, 0, len(rows))
	skipped := 0
	for _, r := range rows {
		pt, err := s.Box.Open(r.BodyCiphertext, r.DekRef, aad(r.ConversationID))
		if err != nil {
			skipped++
			continue
		}
		g, ok := groups[r.PersonaID]
		if !ok {
			g = string(persona.GroupFor(profileEthnicity(r.Profile), r.LanguageMix))
			groups[r.PersonaID] = g
		}
		scam := ""
		if r.ScamType.Valid {
			scam = string(r.ScamType.ScamType)
		}
		msgs = append(msgs, rag.Message{ConversationID: r.ConversationID, PersonaID: r.PersonaID, Group: g, ScamType: scam,
			Inbound: r.Direction == db.MessageDirectionInbound, Text: string(pt)})
	}
	pairs := rag.Build(msgs)
	s.RAG.Replace(pairs)
	s.Log.Info("reply retrieval index rebuilt", "messages", len(rows), "undecryptable", skipped, "examples", len(pairs))
	return nil
}

// RunRAG refreshes the index now and then every `every` until ctx ends.
func (s *Service) RunRAG(ctx context.Context, every time.Duration) {
	if s.RAG == nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.RefreshRAG(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("reply retrieval index refresh failed; keeping the previous one", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// similarReplies finds how we answered messages like the scammer's latest ones in other chats.
func (s *Service) similarReplies(hist []PlainMessage, convID, personaID int64, scamType, group string) []string {
	if s.RAG == nil || s.RAG.Len() == 0 {
		return nil
	}
	var latest []string
	for i := len(hist) - 1; i >= 0 && hist[i].Direction == string(db.MessageDirectionInbound); i-- {
		latest = append([]string{hist[i].Text}, latest...)
	}
	hits := s.RAG.Search(rag.Query{Text: strings.Join(latest, "\n"), ScamType: scamType, Group: group,
		PersonaID: personaID, ConversationID: convID}, ragExamples)
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Reply)
	}
	return out
}

// profileEthnicity reads personas.profile.ethnicity ("" when absent).
func profileEthnicity(profile json.RawMessage) string {
	var pf struct {
		Ethnicity string `json:"ethnicity"`
	}
	_ = json.Unmarshal(profile, &pf)
	return pf.Ethnicity
}
