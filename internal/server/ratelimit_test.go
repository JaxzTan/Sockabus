package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JaxzTan/Buru_AI/internal/demostore"
)

func TestRateLimitGenerate(t *testing.T) {
	h := New(nil, nil).WithLive(nil, nil, testToken, 1).Routes()
	codes := map[int]int{}
	for range tierGen.burst + 3 {
		req := httptest.NewRequest(http.MethodPost, "/internal/personas/generate", nil) // no token: 401 until limited
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes[rec.Code]++
	}
	if codes[http.StatusUnauthorized] != tierGen.burst || codes[http.StatusTooManyRequests] != 3 {
		t.Fatalf("got %v, want %d x 401 then 3 x 429", codes, tierGen.burst)
	}
}

func TestClientIPTrustsForwardedOnlyFromPrivatePeers(t *testing.T) {
	for _, tc := range []struct{ remote, xff, want string }{
		{"203.0.113.9:1000", "1.2.3.4", "203.0.113.9"},               // internet client can't pick its key
		{"172.18.0.5:1000", "9.9.9.9, 198.51.100.7", "198.51.100.7"}, // proxy-appended entry
		{"127.0.0.1:1000", "", "127.0.0.1"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.remote
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := clientIP(r); got != tc.want {
			t.Errorf("remote=%s xff=%q: got %s, want %s", tc.remote, tc.xff, got, tc.want)
		}
	}
}

func TestCORSAllowlist(t *testing.T) {
	h := New(nil, nil).WithLive(nil, nil, testToken, 1).WithCORS([]string{"https://console.example", "*"}).Routes()
	for _, tc := range []struct {
		origin string
		want   string
		code   int
	}{
		{"https://console.example", "https://console.example", http.StatusNoContent},
		{"https://evil.example", "", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodOptions, "/internal/personas", nil)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.code || rec.Header().Get("Access-Control-Allow-Origin") != tc.want {
			t.Errorf("origin %s: code %d ACAO %q, want %d %q", tc.origin, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"), tc.code, tc.want)
		}
	}
}

func TestDemoRoutesNeedTokenWhenSet(t *testing.T) {
	t.Setenv("SOCKABUS_SIMULATE", "0")
	demo, err := demostore.New()
	if err != nil {
		t.Fatal(err)
	}
	h := New(nil, nil).WithDemo(demo).WithOperatorToken(testToken).Routes()
	for _, tc := range []struct {
		path, token string
		want        int
	}{
		{"/internal/personas", "", http.StatusUnauthorized},
		{"/v1/entities", "", http.StatusUnauthorized},
		{"/internal/personas", testToken, http.StatusOK},
		{"/v1/entities", testToken, http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s token=%v: %d, want %d", tc.path, tc.token != "", rec.Code, tc.want)
		}
	}
}
