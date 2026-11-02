package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func claudeAt(t *testing.T, model string, h http.HandlerFunc) *Anthropic {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	a := NewAnthropic("k", model)
	a.URL = srv.URL
	return a
}

var userTurn = Request{Turns: []Turn{{Role: RoleUser, Text: "hi"}}, Effort: EffortLow, MaxOutputTokens: 256}

func TestAnthropicErrorKinds(t *testing.T) {
	cases := []struct {
		status    int
		retry     string
		kind      Kind
		temporary bool
	}{
		{429, "17", KindRateLimited, true},
		{529, "", KindOverloaded, true},
		{500, "", KindUnavailable, true},
		{503, "", KindUnavailable, true},
		{504, "", KindTimeout, true},
		{401, "", KindAuth, false},
		{400, "", KindBadRequest, false},
		{404, "", KindBadRequest, false},
	}
	for _, c := range cases {
		a := claudeAt(t, "claude-haiku-5-5", func(w http.ResponseWriter, _ *http.Request) {
			if c.retry != "" {
				w.Header().Set("retry-after", c.retry)
			}
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"x_error","message":"boom"}}`)
		})
		_, err := a.Generate(context.Background(), userTurn)
		u, ok := AsUpstream(err)
		if !ok || !errors.Is(err, ErrUpstream) {
			t.Fatalf("%d: want *UpstreamError, got %v", c.status, err)
		}
		if u.Kind != c.kind || u.Temporary() != c.temporary || u.Status != c.status || !strings.Contains(u.Message, "boom") {
			t.Errorf("%d: got kind %s temporary %v status %d msg %q", c.status, u.Kind, u.Temporary(), u.Status, u.Message)
		}
		if c.retry != "" && u.RetryAfter != 17*time.Second {
			t.Errorf("retry-after = %v", u.RetryAfter)
		}
	}
}

func TestAnthropicRefusalAndText(t *testing.T) {
	a := claudeAt(t, "claude-haiku-5-5", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"content":[],"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"x"}}`)
	})
	_, err := a.Generate(context.Background(), userTurn)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("refusal must match ErrBlocked, got %v", err)
	}
	if u, _ := AsUpstream(err); u == nil || u.Kind != KindRefused || !strings.Contains(u.Message, "cyber") {
		t.Fatalf("refusal kind: %v", err)
	}

	a = claudeAt(t, "claude-haiku-5-5", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"content":[{"type":"thinking","thinking":""},{"type":"text","text":"`+"```json\\n{\\\"a\\\":1}\\n```"+`"}],"stop_reason":"end_turn"}`)
	})
	out, err := a.Generate(context.Background(), Request{Turns: userTurn.Turns, JSON: true})
	if err != nil || out != `{"a":1}` {
		t.Fatalf("got %q, %v", out, err)
	}

	a = claudeAt(t, "claude-haiku-5-5", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"content":[{"type":"thinking","thinking":""}],"stop_reason":"max_tokens"}`)
	})
	if _, err := a.Generate(context.Background(), userTurn); err == nil {
		t.Fatal("max_tokens with no text must fail")
	} else if u, _ := AsUpstream(err); u == nil || u.Kind != KindTruncated {
		t.Fatalf("truncated kind: %v", err)
	}
}

func TestAnthropicRequestShape(t *testing.T) {
	var got map[string]any
	var beta string
	calls := 0
	a := claudeAt(t, "claude-sonnet-5-5", func(w http.ResponseWriter, r *http.Request) {
		calls++
		beta = r.Header.Get("anthropic-beta")
		got = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		if calls == 1 && got["fallbacks"] != nil {
			w.WriteHeader(400) // org without the beta
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Unexpected value(s) for the anthropic-beta header"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	})
	out, err := a.Generate(context.Background(), userTurn)
	if err != nil || out != "ok" || calls != 2 {
		t.Fatalf("got %q %v after %d calls", out, err, calls)
	}
	if got["fallbacks"] != nil || beta != "" {
		t.Fatalf("fallback must be dropped after a beta 400: %v %q", got["fallbacks"], beta)
	}
	if got["max_tokens"].(float64) < anthropicMinMaxTokens || got["temperature"] != nil {
		t.Fatalf("max_tokens/temperature: %v", got)
	}
	if oc, _ := got["output_config"].(map[string]any); oc["effort"] != "low" {
		t.Fatalf("effort not sent: %v", got["output_config"])
	}
}

func TestRetryingTypedErrors(t *testing.T) {
	f := &flaky{err: &UpstreamError{Provider: "x", Status: 400, Kind: KindBadRequest}}
	if _, err := (Retrying{Inner: f, Attempts: 3}).Generate(context.Background(), Request{}); err == nil || f.calls != 1 {
		t.Fatalf("bad request must not be retried: %d calls", f.calls)
	}
	f = &flaky{err: &UpstreamError{Provider: "x", Status: 529, Kind: KindOverloaded}}
	if _, err := (Retrying{Inner: f, Attempts: 3}).Generate(context.Background(), Request{}); err == nil || f.calls != 3 {
		t.Fatalf("overload must be retried: %d calls", f.calls)
	}
	f = &flaky{err: &UpstreamError{Provider: "x", Status: 429, Kind: KindRateLimited, RetryAfter: time.Minute}}
	if _, err := (Retrying{Inner: f, Attempts: 3}).Generate(context.Background(), Request{}); err == nil || f.calls != 1 {
		t.Fatalf("a long retry-after must return at once: %d calls", f.calls)
	}
}
