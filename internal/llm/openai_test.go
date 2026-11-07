package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatGenerate(t *testing.T) {
	var got oaRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("authorization") != "Bearer k" {
			t.Errorf("path=%s auth=%q", r.URL.Path, r.Header.Get("authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"<think>hmm</think>\n{\"action\":\"reply\",\"bubbles\":[\"ok bang\"]}"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	c := NewOpenAICompat(srv.URL+"/v1/", "k", "mallam")
	out, err := c.Generate(context.Background(), Request{System: "sys", Turns: []Turn{
		{RoleModel, "drop me"}, {RoleUser, "hi"}, {RoleModel, "hai"}, {RoleUser, "transfer now"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"action":"reply","bubbles":["ok bang"]}` {
		t.Fatalf("think block not stripped: %q", out)
	}
	roles := ""
	for _, m := range got.Messages {
		roles += m.Role[:1]
	}
	if got.Model != "mallam" || roles != "suau" {
		t.Fatalf("model=%q roles=%q", got.Model, roles)
	}
}

func TestOpenAICompatErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   Kind
	}{
		{429, `{"error":{"message":"slow down"}}`, KindRateLimited},
		{401, `{"error":{"message":"bad key"}}`, KindAuth},
		{200, `{"choices":[{"message":{"content":""},"finish_reason":"content_filter"}]}`, KindRefused},
		{200, `{"choices":[{"message":{"content":""},"finish_reason":"length"}]}`, KindTruncated},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := NewOpenAICompat(srv.URL, "", "m").Generate(context.Background(), Request{Turns: []Turn{{RoleUser, "hi"}}})
		srv.Close()
		u, ok := AsUpstream(err)
		if !ok || u.Kind != tc.kind {
			t.Errorf("status %d: err=%v, want kind %s", tc.status, err, tc.kind)
		}
		if tc.kind == KindRefused && !errors.Is(err, ErrBlocked) {
			t.Error("content_filter must match ErrBlocked")
		}
	}
}

func TestNewOpenAINeedsURLAndModel(t *testing.T) {
	if _, err := New(Config{Provider: "openai", Model: "m"}); err == nil {
		t.Error("missing base URL must be an error")
	}
	if c, err := New(Config{Provider: "openai", Model: "m", OpenAIBaseURL: "http://x/v1"}); err != nil || c.Name() != "openai-compat/m" {
		t.Errorf("c=%v err=%v", c, err)
	}
}
