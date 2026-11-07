package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gotd/td/telegram/updates"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JaxzTan/Buru_AI/internal/db"
)

// PGState against real PostgreSQL (migration 000010). Skipped without SOCKABUS_TEST_POSTGRES_URL.
func TestPGStateRoundTrip(t *testing.T) {
	url := os.Getenv("SOCKABUS_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("SOCKABUS_TEST_POSTGRES_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	q := db.New(pool)
	p, err := q.CreatePersona(ctx, db.CreatePersonaParams{Name: "State Test", LanguageMix: json.RawMessage(`{"en":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	acc, err := q.CreateHoneypotAccount(ctx, db.CreateHoneypotAccountParams{PersonaID: p.ID, Channel: db.ChannelTelegram, Phone: fmt.Sprintf("state-%d", time.Now().UnixNano()), SessionRef: "test:state"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telegram_update_state WHERE account_id = $1", acc.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM honeypot_accounts WHERE id = $1", acc.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM personas WHERE id = $1", p.ID)
		pool.Close()
	})

	s := NewPGState(q, acc.ID)
	if _, found, err := s.GetState(ctx, 42); err != nil || found {
		t.Fatalf("empty state: found=%v err=%v", found, err)
	}
	if err := s.SetPts(ctx, 42, 7); err != nil { // a partial row is not a usable state
		t.Fatal(err)
	}
	if _, found, _ := s.GetState(ctx, 42); found {
		t.Fatal("partial row reported as found")
	}
	want := updates.State{Pts: 100, Qts: 5, Date: 1730000000, Seq: 9}
	if err := s.SetState(ctx, 42, want); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SetPts(ctx, 42, 101))
	must(s.SetQts(ctx, 42, 6))
	must(s.SetDateSeq(ctx, 42, 1730000100, 10))
	must(s.SetSeq(ctx, 42, 11))
	must(s.SetDate(ctx, 42, 1730000200))
	got, found, err := NewPGState(q, acc.ID).GetState(ctx, 99) // a new process (restart) reads it back
	if err != nil || !found {
		t.Fatalf("after restart: found=%v err=%v", found, err)
	}
	if got != (updates.State{Pts: 101, Qts: 6, Date: 1730000200, Seq: 11}) {
		t.Fatalf("state = %+v", got)
	}
	must(s.SetChannelPts(ctx, 42, 555, 3))
	if pts, ok, _ := s.GetChannelPts(ctx, 42, 555); !ok || pts != 3 {
		t.Fatalf("channel pts %d %v", pts, ok)
	}
	n := 0
	must(s.ForEachChannels(ctx, 42, func(context.Context, int64, int) error { n++; return nil }))
	if n != 1 {
		t.Fatalf("channels = %d", n)
	}
}
