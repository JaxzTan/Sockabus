package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/JaxzTan/Buru_AI/internal/demostore"
	"github.com/JaxzTan/Buru_AI/internal/extraction"
	"github.com/JaxzTan/Buru_AI/internal/risk"
)

func decode(r *http.Request, v any) bool { return json.NewDecoder(r.Body).Decode(v) == nil }

func bad(w http.ResponseWriter, msg string) { writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg}) }

// registerDemo mounts the API the Next.js UIs (web/) call. Customer API: /v1/*. Operator API: /internal/*.
func (s *Server) registerDemo(mux *http.ServeMux) {
	st, eng := s.demo, s.eng
	ok := func(w http.ResponseWriter) { writeJSON(w, 200, map[string]bool{"ok": true}) }

	// --- customer API (DaaS) ---
	mux.HandleFunc("POST /v1/lookup", func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Type, Value, Bank string }
		if !decode(r, &q) || q.Value == "" {
			bad(w, "type and value are required")
			return
		}
		e, found := st.Lookup(q.Type, q.Value)
		if !found {
			writeJSON(w, 200, map[string]any{"match": false})
			return
		}
		conf := "medium" // only verified entities return "high" (PRD FR-9)
		if e.Status == "verified" {
			conf = "high"
		}
		ra := risk.Assess(e.Type, e.Status, conf, int32(e.Sightings)) // same rules as the live API
		writeJSON(w, 200, map[string]any{"match": true, "confidence": conf, "status": e.Status, "scam_types": []string{e.ScamType},
			"first_seen": e.FirstAt.UTC().Format(time.RFC3339), "last_seen": e.LastAt.UTC().Format(time.RFC3339), "sightings": e.Sightings,
			"risk_score": ra.Score, "reason": ra.Reason, "signals": ra.Signals, "recommended_action": ra.Action})
	})
	mux.HandleFunc("GET /v1/entities", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, st.Entities(true)) })
	mux.HandleFunc("GET /v1/weeks", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, st.Weeks()) })
	mux.HandleFunc("GET /v1/usage", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"days": st.Days()}) })

	// --- operator API ---
	mux.HandleFunc("GET /internal/entities", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, st.Entities(false)) })
	mux.HandleFunc("GET /internal/weeks", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, st.Weeks()) })
	mux.HandleFunc("GET /internal/playbooks", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, st.Playbooks()) })
	mux.HandleFunc("GET /internal/conversations", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, st.Convos()) })
	mux.HandleFunc("GET /internal/personas", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, st.Personas()) })
	mux.HandleFunc("GET /internal/accounts", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, st.Accounts()) })

	mux.HandleFunc("POST /internal/entities/{id}/verify", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.PathValue("id"))
		var b struct{ Decision string }
		if !decode(r, &b) || (b.Decision != "verified" && b.Decision != "rejected") {
			bad(w, "decision must be verified or rejected")
			return
		}
		e, found := st.Decide(id, b.Decision)
		if !found {
			writeJSON(w, 404, map[string]string{"error": "entity not found"})
			return
		}
		writeJSON(w, 200, e)
	})
	mux.HandleFunc("POST /internal/conversations/{id}/takeover", func(w http.ResponseWriter, r *http.Request) { eng.Takeover(r.PathValue("id")); ok(w) })
	mux.HandleFunc("POST /internal/conversations/{id}/close", func(w http.ResponseWriter, r *http.Request) { eng.Close(r.PathValue("id")); ok(w) })
	mux.HandleFunc("POST /internal/conversations/{id}/operator-message", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Text string }
		if !decode(r, &b) || b.Text == "" {
			bad(w, "text required")
			return
		}
		eng.Operator(r.PathValue("id"), b.Text)
		ok(w)
	})
	// Feed a real scammer message into a conversation (used by the simulator; handy for curl demos).
	mux.HandleFunc("POST /internal/conversations/{id}/inbound", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Text string }
		if !decode(r, &b) || b.Text == "" {
			bad(w, "text required")
			return
		}
		if !eng.Inbound(r.PathValue("id"), b.Text) {
			writeJSON(w, 404, map[string]string{"error": "conversation not found or closed"})
			return
		}
		ok(w)
	})
	// Run the extractor on any text: curl -d '{"text":"send to Maybank 5621 0384 1192"}' /internal/extract
	mux.HandleFunc("POST /internal/extract", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Text string }
		if !decode(r, &b) {
			bad(w, "text required")
			return
		}
		f := extraction.Extract(b.Text)
		if f == nil {
			f = []extraction.Found{}
		}
		writeJSON(w, 200, map[string]any{"scam_type": extraction.Classify(b.Text), "entities": f})
	})

	mux.HandleFunc("POST /internal/personas", func(w http.ResponseWriter, r *http.Request) {
		var row demostore.Row
		if !decode(r, &row) {
			bad(w, "invalid persona")
			return
		}
		writeJSON(w, 200, st.AddPersona(row))
	})
	mux.HandleFunc("POST /internal/accounts", func(w http.ResponseWriter, r *http.Request) {
		var row demostore.Row
		if !decode(r, &row) {
			bad(w, "invalid account")
			return
		}
		writeJSON(w, 200, st.AddAccount(row))
	})
	mux.HandleFunc("POST /internal/accounts/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Status string }
		if !decode(r, &b) {
			bad(w, "status required")
			return
		}
		st.SetAccountStatus(r.PathValue("id"), b.Status)
		ok(w)
	})
	mux.HandleFunc("DELETE /internal/accounts/{id}", func(w http.ResponseWriter, r *http.Request) { st.RemoveAccount(r.PathValue("id")); ok(w) })
}
