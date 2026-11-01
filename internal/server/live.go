package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/JaxzTan/Buru_AI/internal/channel/sim"
	"github.com/JaxzTan/Buru_AI/internal/db"
	"github.com/JaxzTan/Buru_AI/internal/honeypot"
	"github.com/JaxzTan/Buru_AI/internal/llm"
	"github.com/JaxzTan/Buru_AI/internal/persona"
)

// liveAPI is the operator API (TRD §5, /internal/*) and lookup (/v1/lookup) on PostgreSQL.
//
// Responses carry scammer-supplied text as plain JSON strings. Clients must render them as text
// only (no HTML/markdown, no auto-linking; show URLs defanged), per TRD §7.
type liveAPI struct {
	svc        *honeypot.Service
	connect    honeypot.AccountConnector
	token      string
	operatorID int64
	sim        *sim.Hub // development chat simulator; nil when disabled
	src        StatusSources
}

func (a *liveAPI) register(mux *http.ServeMux) {
	h := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, a.auth(fn)) }

	// Customer lookup (FR-10). M3 replaces the operator token with per-customer API keys (FR-13).
	h("POST /v1/lookup", a.lookup)

	h("GET /internal/personas", a.listPersonas)
	h("POST /internal/personas", a.createPersona)
	h("POST /internal/personas/generate", a.generatePersonas)
	h("POST /internal/personas/{id}/status", a.setPersonaStatus)
	h("POST /internal/personas/{id}/prompt", a.newPromptVersion)
	h("PUT /internal/personas/{id}/playbooks", a.setPersonaPlaybooks)
	h("GET /internal/playbooks", a.listPlaybooks)

	h("GET /internal/accounts", a.listAccounts)
	h("POST /internal/accounts", a.bindAccount)
	h("POST /internal/accounts/{id}/connect", a.connectAccount)
	h("POST /internal/accounts/{id}/status", a.setAccountStatus)

	h("GET /internal/conversations", a.listConversations)
	h("GET /internal/conversations/{id}", a.getConversation)
	h("POST /internal/conversations/{id}/takeover", a.takeover)
	h("POST /internal/conversations/{id}/operator-message", a.operatorMessage)
	h("POST /internal/conversations/{id}/close", a.closeConversation)

	h("GET /internal/entities", a.listEntities)
	h("POST /internal/entities/{id}/verify", a.verify)

	h("GET /internal/status", a.getStatus)

	if a.sim != nil {
		h("POST /internal/sim/accounts", a.simAccount)
		h("POST /internal/sim/messages", a.simMessage)
		h("GET /internal/sim/events", a.simEvents)
	}
}

// ---- development chat simulator ----

func (a *liveAPI) simAccount(w http.ResponseWriter, r *http.Request) {
	var b struct {
		PersonaID int64 `json:"persona_id"`
	}
	if err := readJSON(r, &b); err != nil {
		badJSON(w, err)
		return
	}
	acct, err := a.svc.SimAccount(r.Context(), b.PersonaID)
	if err != nil {
		fail(w, r, err)
		return
	}
	p, err := a.svc.Q.GetPersona(r.Context(), acct.PersonaID)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account_id": acct.ID, "persona": p.Name, "persona_status": p.Status})
}

func (a *liveAPI) simMessage(w http.ResponseWriter, r *http.Request) {
	var b struct {
		AccountID int64  `json:"account_id"`
		From      string `json:"from"`
		Text      string `json:"text"`
	}
	if err := readJSON(r, &b); err != nil {
		badJSON(w, err)
		return
	}
	if err := a.svc.SimInbound(r.Context(), b.AccountID, b.From, b.Text); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// simEvents long-polls (up to 25 s) for the persona's messages, typing and read receipts.
func (a *liveAPI) simEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	acct, err1 := strconv.ParseInt(q.Get("account_id"), 10, 64)
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	if err1 != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id required"})
		return
	}
	peer, _ := honeypot.SimPeer(q.Get("from"))
	evs := a.sim.Events(r.Context(), acct, peer, after, 25*time.Second)
	if evs == nil {
		evs = []sim.Event{}
	}
	writeJSON(w, http.StatusOK, evs)
}

// ---- status (NFR-9) ----

// StatusSources feed GET /internal/status. A nil field is reported as absent, not as an error.
type StatusSources struct {
	Telegram  TelegramStatus                       // nil when the Telegram connector is disabled
	LLMErrors func() int                           // failed LLM calls in the recent window
	Workflows func(context.Context) (int64, error) // running Temporal workflows
}

// TelegramStatus is implemented by telegram.Manager.
type TelegramStatus interface {
	Status() (connected bool, lastMessageAt time.Time)
}

func (a *liveAPI) getStatus(w http.ResponseWriter, r *http.Request) {
	var out struct {
		Telegram struct {
			Connected     bool       `json:"connected"`
			LastMessageAt *time.Time `json:"last_message_at"`
		} `json:"telegram"`
		LLM struct {
			ErrorsRecent int `json:"errors_recent"`
		} `json:"llm"`
		Workflows struct {
			Running *int64 `json:"running"`
			Error   string `json:"error,omitempty"`
		} `json:"workflows"`
	}
	if a.src.Telegram != nil {
		connected, last := a.src.Telegram.Status()
		out.Telegram.Connected = connected
		if !last.IsZero() {
			out.Telegram.LastMessageAt = &last
		}
	}
	if a.src.LLMErrors != nil {
		out.LLM.ErrorsRecent = a.src.LLMErrors()
	}
	if a.src.Workflows != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		n, err := a.src.Workflows(ctx)
		cancel()
		if err != nil {
			slog.Warn("status: cannot count running workflows", "err", err)
			out.Workflows.Error = "temporal unavailable"
		} else {
			out.Workflows.Running = &n
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *liveAPI) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || a.token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid bearer token"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next(w, r)
	})
}

// ---- helpers ----

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be a positive integer"})
		return 0, false
	}
	return id, true
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, honeypot.ErrBadRequest):
		status = http.StatusBadRequest
	case errors.Is(err, honeypot.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, honeypot.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, llm.ErrNoClient):
		status = http.StatusServiceUnavailable
		err = errors.New("no LLM configured: set GEMINI_API_KEY (or SOCKABUS_LLM_PROVIDER=anthropic with ANTHROPIC_API_KEY)")
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		slog.Error("operator api error", "path", r.URL.Path, "err", err)
		msg = "internal error"
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

func badJSON(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body: " + err.Error()})
}

func limit(r *http.Request, def int32) int32 {
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
		return int32(n)
	}
	return def
}

// ---- lookup ----

func (a *liveAPI) lookup(w http.ResponseWriter, r *http.Request) {
	var req honeypot.LookupRequest
	if err := readJSON(r, &req); err != nil {
		badJSON(w, err)
		return
	}
	res, err := a.svc.Lookup(r.Context(), req)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- personas ----

func (a *liveAPI) listPersonas(w http.ResponseWriter, r *http.Request) {
	out, err := a.svc.Personas(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *liveAPI) createPersona(w http.ResponseWriter, r *http.Request) {
	var in honeypot.PersonaInput
	if err := readJSON(r, &in); err != nil {
		badJSON(w, err)
		return
	}
	out, err := a.svc.CreatePersona(r.Context(), in, a.operatorID)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (a *liveAPI) generatePersonas(w http.ResponseWriter, r *http.Request) {
	var req persona.GenerateRequest
	if err := readJSON(r, &req); err != nil {
		badJSON(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	out, notes, err := a.svc.GeneratePersonas(ctx, req, a.operatorID)
	if err != nil && len(out) == 0 {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"personas": out, "notes": notes})
}

func (a *liveAPI) setPersonaStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &b); err != nil {
		badJSON(w, err)
		return
	}
	if err := a.svc.SetPersonaStatus(r.Context(), id, b.Status); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": b.Status})
}

func (a *liveAPI) newPromptVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		SystemPrompt string `json:"system_prompt"`
	}
	if err := readJSON(r, &b); err != nil {
		badJSON(w, err)
		return
	}
	pv, err := a.svc.NewPromptVersion(r.Context(), id, a.operatorID, b.SystemPrompt)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, pv)
}

func (a *liveAPI) setPersonaPlaybooks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		ScamTypes []string `json:"scam_types"`
	}
	if err := readJSON(r, &b); err != nil {
		badJSON(w, err)
		return
	}
	if err := a.svc.SetPersonaPlaybooks(r.Context(), id, b.ScamTypes); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "scam_types": b.ScamTypes})
}

func (a *liveAPI) listPlaybooks(w http.ResponseWriter, r *http.Request) {
	out, err := a.svc.Q.ListPlaybooks(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- accounts ----

func (a *liveAPI) listAccounts(w http.ResponseWriter, r *http.Request) {
	out, err := a.svc.Q.ListHoneypotAccounts(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *liveAPI) bindAccount(w http.ResponseWriter, r *http.Request) {
	var in honeypot.AccountInput
	if err := readJSON(r, &in); err != nil {
		badJSON(w, err)
		return
	}
	acct, err := a.svc.BindAccount(r.Context(), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	resp := map[string]any{"account": acct, "connected": false}
	if a.connect == nil {
		resp["connect_error"] = "Telegram connector disabled: set SOCKABUS_TG_APP_ID and SOCKABUS_TG_APP_HASH and restart"
	} else {
		if err := a.connect(context.WithoutCancel(r.Context()), acct); err != nil {
			resp["connect_error"] = err.Error()
		} else {
			resp["connected"] = true
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (a *liveAPI) connectAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	acct, err := a.svc.Q.GetHoneypotAccount(r.Context(), id)
	if err != nil {
		fail(w, r, honeypot.ErrNotFound)
		return
	}
	if a.connect == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Telegram connector disabled: set SOCKABUS_TG_APP_ID and SOCKABUS_TG_APP_HASH and restart"})
		return
	}
	if err := a.connect(context.WithoutCancel(r.Context()), acct); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "connected": true})
}

func (a *liveAPI) setAccountStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Status        string     `json:"status"`
		CooldownUntil *time.Time `json:"cooldown_until"`
	}
	if err := readJSON(r, &b); err != nil {
		badJSON(w, err)
		return
	}
	if err := a.svc.SetAccountStatus(r.Context(), id, b.Status, b.CooldownUntil); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": b.Status})
}

// ---- conversations ----

func (a *liveAPI) listConversations(w http.ResponseWriter, r *http.Request) {
	p := db.ListConversationsWithPersonaParams{RowLimit: limit(r, 100)}
	if s := r.URL.Query().Get("status"); s != "" {
		st := db.ConversationStatus(s)
		if !st.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be bot, human or closed"})
			return
		}
		p.Status = db.NullConversationStatus{ConversationStatus: st, Valid: true}
	}
	out, err := a.svc.Q.ListConversationsWithPersona(r.Context(), p)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *liveAPI) getConversation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := a.svc.Conversation(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// takeover body: {"mode":"human"} to take over, {"mode":"bot"} to hand back. Empty body toggles.
func (a *liveAPI) takeover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &b); err != nil {
		badJSON(w, err)
		return
	}
	human := b.Mode == "human"
	if b.Mode == "" {
		conv, err := a.svc.Q.GetConversation(r.Context(), id)
		if err != nil {
			fail(w, r, honeypot.ErrNotFound)
			return
		}
		human = conv.Status == db.ConversationStatusBot
	} else if b.Mode != "bot" && b.Mode != "human" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `mode must be "human" or "bot"`})
		return
	}
	conv, err := a.svc.SetControl(r.Context(), id, human)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": conv.Status})
}

func (a *liveAPI) operatorMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &b); err != nil {
		badJSON(w, err)
		return
	}
	m, err := a.svc.OperatorSend(r.Context(), id, a.operatorID, b.Text)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (a *liveAPI) closeConversation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.svc.Close(r.Context(), id); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "closed"})
}

// ---- verification queue (FR-9, TR-9) ----

func (a *liveAPI) listEntities(w http.ResponseWriter, r *http.Request) {
	p := db.ListEntitiesParams{RowLimit: limit(r, 200)}
	if s := r.URL.Query().Get("status"); s != "" {
		st := db.EntityStatus(s)
		if !st.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be unverified, verified or rejected"})
			return
		}
		p.Status = db.NullEntityStatus{EntityStatus: st, Valid: true}
	}
	out, err := a.svc.Q.ListEntities(r.Context(), p)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *liveAPI) verify(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := readJSON(r, &b); err != nil {
		badJSON(w, err)
		return
	}
	e, err := a.svc.Verify(r.Context(), id, a.operatorID, b.Decision, b.Note)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}
