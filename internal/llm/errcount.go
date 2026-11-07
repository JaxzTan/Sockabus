package llm

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrorCounter counts failed LLM calls over a rolling window for GET /internal/status (NFR-9:
// "recent LLM errors"). One counter can wrap several clients, so persona and extraction errors
// add up. Safe for concurrent use.
type ErrorCounter struct {
	Window time.Duration // zero means 15 minutes

	mu  sync.Mutex
	at  []time.Time
	now func() time.Time // tests only
}

// Wrap returns inner with failures recorded on c. A nil inner stays nil, so callers can keep
// their "no LLM configured" checks.
func (c *ErrorCounter) Wrap(inner Client) Client {
	if inner == nil {
		return nil
	}
	return counted{Client: inner, c: c}
}

// Recent is the number of failed calls inside the window.
func (c *ErrorCounter) Recent() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	return len(c.at)
}

func (c *ErrorCounter) record() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = append(c.at, c.clock())
	c.prune()
}

func (c *ErrorCounter) prune() {
	window := c.Window
	if window <= 0 {
		window = 15 * time.Minute
	}
	cutoff := c.clock().Add(-window)
	i := 0
	for i < len(c.at) && !c.at[i].After(cutoff) {
		i++
	}
	c.at = c.at[i:]
}

func (c *ErrorCounter) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

type counted struct {
	Client
	c *ErrorCounter
}

// Generate counts every error except the caller giving up (context.Canceled).
func (k counted) Generate(ctx context.Context, req Request) (string, error) {
	out, err := k.Client.Generate(ctx, req)
	if err != nil && !errors.Is(err, context.Canceled) {
		k.c.record()
	}
	return out, err
}
