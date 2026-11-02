package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JaxzTan/Buru_AI/internal/honeypot"
	"github.com/JaxzTan/Buru_AI/internal/llm"
)

func TestFailMapsLLMErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		want   string
		retry  string
	}{
		{fmt.Errorf("generate: %w", &llm.UpstreamError{Provider: "anthropic", Status: 429, Kind: llm.KindRateLimited, RetryAfter: 20 * time.Second}), 503, "LLM provider unavailable: rate limited, retry in 20 s", "20"},
		{&llm.UpstreamError{Provider: "anthropic", Status: 529, Kind: llm.KindOverloaded}, 503, "LLM provider unavailable: overloaded", ""},
		{&llm.UpstreamError{Provider: "anthropic", Status: 500, Kind: llm.KindUnavailable}, 503, "LLM provider unavailable: HTTP 500", ""},
		{&llm.UpstreamError{Provider: "anthropic", Kind: llm.KindTimeout, Err: context.DeadlineExceeded}, 504, "LLM provider timed out", ""},
		{&llm.UpstreamError{Provider: "anthropic", Status: 401, Kind: llm.KindAuth}, 502, "rejected the API key", ""},
		{&llm.UpstreamError{Provider: "anthropic", Status: 200, Kind: llm.KindRefused, Message: "declined (category cyber)", Err: llm.ErrBlocked}, 502, "declined", ""},
		{llm.ErrNoClient, 503, "ANTHROPIC_API_KEY", ""},
		{honeypot.ErrNotFound, 404, "not found", ""},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), 504, "timed out", ""},
		{fmt.Errorf("db exploded"), 500, "internal error", ""},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		fail(w, httptest.NewRequest(http.MethodPost, "/internal/personas/generate", nil), c.err)
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != c.status || !strings.Contains(body["error"], c.want) {
			t.Errorf("%v: got %d %q, want %d containing %q", c.err, w.Code, body["error"], c.status, c.want)
		}
		if got := w.Header().Get("Retry-After"); got != c.retry {
			t.Errorf("%v: Retry-After %q, want %q", c.err, got, c.retry)
		}
	}
}
