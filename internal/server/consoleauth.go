package server

import (
	"net/http"
	"strings"
)

// consoleGoogleEmails are the only Google accounts allowed to sign in to the console. The Next.js
// callback (web/src/app/api/auth/google/callback) asks POST /internal/auth/google/check after its own
// env allowlist, and refuses the sign-in unless this list says yes, so a misconfigured
// SOCKABUS_CONSOLE_GOOGLE_ALLOWED_EMAILS/_DOMAIN alone can't let anyone else in.
var consoleGoogleEmails = map[string]bool{
	"jaxz8835@gmail.com":       true,
	"jaycigan@gmail.com":       true,
	"ganwx88@gmail.com":        true,
	"andywong562006@gmail.com": true,
	"horjianjia33@gmail.com":   true,
}

// googleEmailAllowed reports whether email is on the console allowlist: exact match after trimming
// and lowercasing. Anything else, including an empty string, is refused.
func googleEmailAllowed(email string) bool {
	e := strings.ToLower(strings.TrimSpace(email))
	return e != "" && consoleGoogleEmails[e]
}

// googleCheck answers POST /internal/auth/google/check {"email": "..."}:
// 200 {"allowed":true}, or 403 {"allowed":false} (also for a malformed body).
func googleCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := readJSON(r, &in); err != nil || !googleEmailAllowed(in.Email) {
		writeJSON(w, http.StatusForbidden, map[string]bool{"allowed": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"allowed": true})
}
