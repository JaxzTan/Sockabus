package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubClient struct{ err error }

func (s stubClient) Name() string { return "stub" }
func (s stubClient) Generate(context.Context, Request) (string, error) {
	return "ok", s.err
}

func TestErrorCounterWindow(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	c := &ErrorCounter{Window: 15 * time.Minute, now: func() time.Time { return now }}
	failing := c.Wrap(stubClient{err: errors.New("503")})
	ok := c.Wrap(stubClient{})
	canceled := c.Wrap(stubClient{err: context.Canceled})

	_, _ = failing.Generate(context.Background(), Request{})
	_, _ = ok.Generate(context.Background(), Request{})
	_, _ = canceled.Generate(context.Background(), Request{})
	now = now.Add(10 * time.Minute)
	_, _ = failing.Generate(context.Background(), Request{})
	if got := c.Recent(); got != 2 {
		t.Fatalf("Recent() = %d, want 2 (success and cancel are not errors)", got)
	}
	now = now.Add(6 * time.Minute) // first error is now 16 min old
	if got := c.Recent(); got != 1 {
		t.Fatalf("Recent() = %d after the window moved, want 1", got)
	}
	if c.Wrap(nil) != nil {
		t.Fatal("Wrap(nil) must stay nil")
	}
}
