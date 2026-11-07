package telegram

import (
	"context"
	"errors"
	"sync"

	"github.com/gotd/td/telegram/updates"
	"github.com/jackc/pgx/v5"

	"github.com/JaxzTan/Buru_AI/internal/db"
)

// PGState is a gotd updates.StateStorage that keeps one honeypot account's common update state
// (pts, qts, date, seq) in telegram_update_state (TRD G-9, migration 000010), so after a restart
// gotd asks Telegram for the difference and replays messages that arrived while the app was down
// (inbound dedupe keys make replays idempotent). Channel pts stay in memory: honeypots only talk
// in private chats.
//
// gotd passes the Telegram user id; the row is keyed by honeypot_accounts.id instead, so a
// re-login of the same account keeps its row.
type PGState struct {
	Q         *db.Queries
	AccountID int64

	mu       sync.Mutex
	channels map[int64]int
}

var _ updates.StateStorage = (*PGState)(nil)

func NewPGState(q *db.Queries, accountID int64) *PGState {
	return &PGState{Q: q, AccountID: accountID, channels: map[int64]int{}}
}

func (s *PGState) GetState(ctx context.Context, _ int64) (updates.State, bool, error) {
	row, err := s.Q.GetTelegramUpdateState(ctx, s.AccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return updates.State{}, false, nil
	}
	if err != nil {
		return updates.State{}, false, err
	}
	if row.Date == 0 {
		return updates.State{}, false, nil // partial row: let gotd fetch the remote state
	}
	return updates.State{Pts: int(row.Pts), Qts: int(row.Qts), Date: int(row.Date), Seq: int(row.Seq)}, true, nil
}

func (s *PGState) SetState(ctx context.Context, _ int64, st updates.State) error {
	return s.Q.SetTelegramUpdateState(ctx, db.SetTelegramUpdateStateParams{AccountID: s.AccountID,
		Pts: int32(st.Pts), Qts: int32(st.Qts), Date: int32(st.Date), Seq: int32(st.Seq)})
}

func (s *PGState) SetPts(ctx context.Context, _ int64, pts int) error {
	return s.Q.SetTelegramPts(ctx, db.SetTelegramPtsParams{AccountID: s.AccountID, Pts: int32(pts)})
}

func (s *PGState) SetQts(ctx context.Context, _ int64, qts int) error {
	return s.Q.SetTelegramQts(ctx, db.SetTelegramQtsParams{AccountID: s.AccountID, Qts: int32(qts)})
}

func (s *PGState) SetDate(ctx context.Context, _ int64, date int) error {
	return s.Q.SetTelegramDate(ctx, db.SetTelegramDateParams{AccountID: s.AccountID, Date: int32(date)})
}

func (s *PGState) SetSeq(ctx context.Context, _ int64, seq int) error {
	return s.Q.SetTelegramSeq(ctx, db.SetTelegramSeqParams{AccountID: s.AccountID, Seq: int32(seq)})
}

func (s *PGState) SetDateSeq(ctx context.Context, _ int64, date, seq int) error {
	return s.Q.SetTelegramDateSeq(ctx, db.SetTelegramDateSeqParams{AccountID: s.AccountID, Date: int32(date), Seq: int32(seq)})
}

func (s *PGState) GetChannelPts(_ context.Context, _, channelID int64) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pts, ok := s.channels[channelID]
	return pts, ok, nil
}

func (s *PGState) SetChannelPts(_ context.Context, _, channelID int64, pts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels[channelID] = pts
	return nil
}

func (s *PGState) ForEachChannels(ctx context.Context, _ int64, f func(ctx context.Context, channelID int64, pts int) error) error {
	s.mu.Lock()
	snapshot := make(map[int64]int, len(s.channels))
	for k, v := range s.channels {
		snapshot[k] = v
	}
	s.mu.Unlock()
	for id, pts := range snapshot {
		if err := f(ctx, id, pts); err != nil {
			return err
		}
	}
	return nil
}
