package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeTelegram struct {
	connected bool
	last      time.Time
}

func (f fakeTelegram) Status() (bool, time.Time) { return f.connected, f.last }

const testToken = "test-token-0123456789abcdef"

func getStatus(t *testing.T, src StatusSources, token string) (int, map[string]map[string]any) {
	t.Helper()
	h := New(nil, nil).WithLive(nil, nil, testToken, 1).WithStatus(src).Routes()
	req := httptest.NewRequest(http.MethodGet, "/internal/status", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestStatusRequiresToken(t *testing.T) {
	for _, tok := range []string{"", "wrong-token"} {
		if code, _ := getStatus(t, StatusSources{}, tok); code != http.StatusUnauthorized {
			t.Errorf("token %q: status %d, want 401", tok, code)
		}
	}
}

func TestStatusReportsSources(t *testing.T) {
	last := time.Date(2026, 10, 31, 8, 41, 0, 0, time.UTC)
	code, body := getStatus(t, StatusSources{
		Telegram:  fakeTelegram{connected: true, last: last},
		LLMErrors: func() int { return 2 },
		Workflows: func(context.Context) (int64, error) { return 3, nil },
	}, testToken)
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if body["telegram"]["connected"] != true || body["telegram"]["last_message_at"] != "2026-10-31T08:41:00Z" {
		t.Errorf("telegram = %v", body["telegram"])
	}
	if body["llm"]["errors_recent"] != float64(2) {
		t.Errorf("llm = %v", body["llm"])
	}
	if body["workflows"]["running"] != float64(3) {
		t.Errorf("workflows = %v", body["workflows"])
	}
}

func TestStatusWithoutConnectorOrTemporal(t *testing.T) {
	code, body := getStatus(t, StatusSources{
		Workflows: func(context.Context) (int64, error) { return 0, errors.New("dial tcp: refused") },
	}, testToken)
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if body["telegram"]["connected"] != false || body["telegram"]["last_message_at"] != nil {
		t.Errorf("telegram = %v, want disconnected with null last_message_at", body["telegram"])
	}
	if body["workflows"]["running"] != nil || body["workflows"]["error"] != "temporal unavailable" {
		t.Errorf("workflows = %v", body["workflows"])
	}
}
