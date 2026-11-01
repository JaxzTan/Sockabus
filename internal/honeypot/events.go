package honeypot

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JaxzTan/Buru_AI/internal/db"
)

// Scam phase history (conversation_stage_events) and account health history
// (account_health_events). Both tables are append-only (migrations 000008, 000010).

const (
	StageSourceBot        = "bot"        // playbook stage machine (FR-4)
	StageSourceClassifier = "classifier" // scam-type classifier (FR-7)
	StageSourceOperator   = "operator"
)

// setStage changes conversations.stage and appends the history row in one transaction.
func (s *Service) setStage(ctx context.Context, conv db.Conversation, next, source string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := s.Q.WithTx(tx)
		if err := q.SetConversationStage(ctx, db.SetConversationStageParams{ID: conv.ID, Stage: next}); err != nil {
			return err
		}
		_, err := q.InsertStageEvent(ctx, db.InsertStageEventParams{ConversationID: conv.ID,
			FromStage: pgtype.Text{String: conv.Stage, Valid: true}, ToStage: next,
			FromScamType: conv.ScamType, ScamType: conv.ScamType, Source: source})
		return err
	})
}

// setScamTypeRow changes conversations.scam_type and appends the history row in one transaction.
func (s *Service) setScamTypeRow(ctx context.Context, conv db.Conversation, label db.NullScamType, source string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := s.Q.WithTx(tx)
		if err := q.SetConversationScamType(ctx, db.SetConversationScamTypeParams{ID: conv.ID, ScamType: label}); err != nil {
			return err
		}
		stage := pgtype.Text{String: conv.Stage, Valid: true}
		_, err := q.InsertStageEvent(ctx, db.InsertStageEventParams{ConversationID: conv.ID,
			FromStage: stage, ToStage: conv.Stage, FromScamType: conv.ScamType, ScamType: label, Source: source})
		return err
	})
}

// StageEventView is one row of GET /internal/conversations/{id}/stages.
type StageEventView struct {
	ID           int64     `json:"id"`
	FromStage    *string   `json:"from_stage"`
	ToStage      string    `json:"to_stage"`
	FromScamType *string   `json:"from_scam_type"`
	ScamType     *string   `json:"scam_type"`
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
}

type StageHistory struct {
	ConversationID int64            `json:"conversation_id"`
	Stage          string           `json:"stage"`     // current stage
	ScamType       *string          `json:"scam_type"` // current scam type
	Events         []StageEventView `json:"events"`
}

func (s *Service) StageHistory(ctx context.Context, convID int64) (StageHistory, error) {
	conv, err := s.Q.GetConversation(ctx, convID)
	if err != nil {
		return StageHistory{}, notFound(err)
	}
	rows, err := s.Q.ListStageEvents(ctx, convID)
	if err != nil {
		return StageHistory{}, err
	}
	out := StageHistory{ConversationID: convID, Stage: conv.Stage, ScamType: scamPtr(conv.ScamType), Events: make([]StageEventView, 0, len(rows))}
	for _, r := range rows {
		v := StageEventView{ID: r.ID, ToStage: r.ToStage, FromScamType: scamPtr(r.FromScamType), ScamType: scamPtr(r.ScamType), Source: r.Source, CreatedAt: r.CreatedAt}
		if r.FromStage.Valid {
			f := r.FromStage.String
			v.FromStage = &f
		}
		out.Events = append(out.Events, v)
	}
	return out, nil
}

// ---- account health (G-10) ----

// Health event names (account_health_events.event CHECK list).
const (
	HealthConnected    = "connected"
	HealthDisconnected = "disconnected"
	HealthConnectError = "connect_error"
	HealthUnauthorized = "unauthorized"
	HealthBanned       = "banned"
	HealthFloodWait    = "flood_wait"
	HealthCooldown     = "cooldown"
	HealthActive       = "active"
)

func healthEventForStatus(st db.AccountStatus) string {
	switch st {
	case db.AccountStatusBanned:
		return HealthBanned
	case db.AccountStatusCooldown:
		return HealthCooldown
	default:
		return HealthActive
	}
}

// RecordAccountHealth appends a health event. Best effort: it never fails the caller, and it
// runs on its own short deadline so a slow database cannot stall the Telegram connector.
func (s *Service) RecordAccountHealth(ctx context.Context, accountID int64, event string, detail map[string]any) {
	var raw json.RawMessage
	if len(detail) > 0 {
		raw, _ = json.Marshal(detail)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if _, err := s.Q.InsertAccountHealthEvent(ctx, db.InsertAccountHealthEventParams{AccountID: accountID, Event: event, Detail: raw}); err != nil {
		s.Log.Warn("account health event not recorded", "account_id", accountID, "event", event, "err", err)
	}
}

// AccountHealth returns the newest health events of an account.
func (s *Service) AccountHealth(ctx context.Context, accountID int64, limit int32) ([]db.AccountHealthEvent, error) {
	if _, err := s.Q.GetHoneypotAccount(ctx, accountID); err != nil {
		return nil, notFound(err)
	}
	return s.Q.ListAccountHealthEvents(ctx, db.ListAccountHealthEventsParams{AccountID: accountID, Limit: limit})
}
