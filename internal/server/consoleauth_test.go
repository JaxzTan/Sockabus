package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JaxzTan/Buru_AI/internal/demostore"
)

func TestGoogleEmailAllowed(t *testing.T) {
	for _, tc := range []struct {
		email string
		want  bool
	}{
		{"jaxz8835@gmail.com", true},
		{"jaycigan@gmail.com", true},
		{"ganwx88@gmail.com", true},
		{"andywong562006@gmail.com", true},
		{"horjianjia33@gmail.com", true},
		{"JAXZ8835@GMAIL.COM", true},
		{"  HorJianJia33@Gmail.com \n", true},
		{"\tganwx88@gmail.com", true},
		{"someone.else@gmail.com", false},
		{"jaxz8835@googlemail.com", false},
		{"jaxz8835@gmail.com.evil.com", false},
		{"evil.jaxz8835@gmail.com", false},
		{"jaxz8835@gmail.co", false},
		{"jaxz8835", false},
		{"jaxz8835@gmail.com,someone@gmail.com", false},
		{"", false},
		{"   ", false},
	} {
		if got := googleEmailAllowed(tc.email); got != tc.want {
			t.Errorf("googleEmailAllowed(%q) = %v, want %v", tc.email, got, tc.want)
		}
	}
}

func TestGoogleCheckEndpoint(t *testing.T) {
	demo, err := demostore.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOCKABUS_SIMULATE", "0")
	modes := map[string]http.Handler{
		"live":          New(nil, nil).WithLive(nil, nil, testToken, 1).Routes(),
		"demo":          New(nil, nil).WithDemo(demo).WithOperatorToken(testToken).Routes(),
		"demo_no_token": New(nil, nil).WithDemo(demo).Routes(),
	}
	for _, tc := range []struct {
		mode, token, body string
		want              int
	}{
		{"live", "", `{"email":"jaxz8835@gmail.com"}`, http.StatusUnauthorized},
		{"live", "wrong-token", `{"email":"jaxz8835@gmail.com"}`, http.StatusUnauthorized},
		{"live", testToken, `{"email":"jaxz8835@gmail.com"}`, http.StatusOK},
		{"live", testToken, `{"email":" AndyWong562006@gmail.com "}`, http.StatusOK},
		{"live", testToken, `{"email":"random.person@gmail.com"}`, http.StatusForbidden},
		{"live", testToken, `{"email":"jaxz8835@gmail.com.evil.com"}`, http.StatusForbidden},
		{"live", testToken, `{"email":""}`, http.StatusForbidden},
		{"live", testToken, ``, http.StatusForbidden},
		{"live", testToken, `not json`, http.StatusForbidden},
		{"live", testToken, `{"email":"jaxz8835@gmail.com","admin":true}`, http.StatusForbidden},
		{"demo", "", `{"email":"jaxz8835@gmail.com"}`, http.StatusUnauthorized},
		{"demo", testToken, `{"email":"ganwx88@gmail.com"}`, http.StatusOK},
		{"demo", testToken, `{"email":"random.person@gmail.com"}`, http.StatusForbidden},
		{"demo_no_token", "", `{"email":"jaxz8835@gmail.com"}`, http.StatusUnauthorized},
	} {
		req := httptest.NewRequest(http.MethodPost, "/internal/auth/google/check", strings.NewReader(tc.body))
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		rec := httptest.NewRecorder()
		modes[tc.mode].ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s token=%q body=%q: status %d, want %d (%s)", tc.mode, tc.token, tc.body, rec.Code, tc.want, rec.Body)
			continue
		}
		wantBody := map[int]string{200: `{"allowed":true}`, 403: `{"allowed":false}`}[tc.want]
		if wantBody != "" && strings.TrimSpace(rec.Body.String()) != wantBody {
			t.Errorf("%s body=%q: response %s, want %s", tc.mode, tc.body, rec.Body, wantBody)
		}
	}
}
