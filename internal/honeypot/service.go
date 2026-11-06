// Package honeypot is the live persona engine: it routes inbound channel messages to conversation
// workflows (FR-2, TR-2), runs the workflow activities (persona replies, extraction, stage machine),
// and carries out operator actions (takeover, operator messages, verification).
package honeypot

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/JaxzTan/Buru_AI/internal/channel/sim"
	"github.com/JaxzTan/Buru_AI/internal/db"
	"github.com/JaxzTan/Buru_AI/internal/gateway"
	"github.com/JaxzTan/Buru_AI/internal/llm"
	"github.com/JaxzTan/Buru_AI/internal/msgcrypt"
	"github.com/JaxzTan/Buru_AI/internal/workflows"
)

type Config struct {
	TaskQueue string
	Timing    workflows.Timing
	// PlaintextIdentifiers keeps writing conversations.scammer_handle / scammer_peer_ref and
	// personas.demo_identifiers in clear next to their ciphertext (000012 phase 1). Turn it off
	// (SOCKABUS_IDENTIFIER_PLAINTEXT=0) before applying the pending drop_plaintext_identifiers migration.
	PlaintextIdentifiers bool
}

type Service struct {
	Pool       *pgxpool.Pool
	Q          *db.Queries
	Box        *msgcrypt.Box
	PersonaLLM llm.Client // nil → playbook fallback lines
	ExtractLLM llm.Client // nil → regex extraction + keyword classifier only
	GenLLM     llm.Client // persona generation: long per-try timeout; nil → PersonaLLM
	SummaryLLM llm.Client // AI summaries (TR-7): extract model, long per-try timeout; nil → GenLLM
	Temporal   client.Client
	Senders    *gateway.Registry
	Sim        gateway.Sender // dev-only simulated channel (internal/channel/sim); nil when disabled
	Cfg        Config
	Log        *slog.Logger
	Now        func() time.Time
}

func NewService(pool *pgxpool.Pool, box *msgcrypt.Box, personaLLM, extractLLM llm.Client, tc client.Client, senders *gateway.Registry, cfg Config, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{Pool: pool, Q: db.New(pool), Box: box, PersonaLLM: personaLLM, ExtractLLM: extractLLM,
		Temporal: tc, Senders: senders, Cfg: cfg, Log: log.With("component", "honeypot"), Now: func() time.Time { return time.Now().UTC() }}
}

var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrBadRequest  = errors.New("bad request")
	errNotRoutable = errors.New("not routable")
)

func aad(conversationID int64) []byte {
	return []byte("conversation:" + strconv.FormatInt(conversationID, 10))
}

// HandleInbound implements gateway.InboundHandler: account → persona → conversation → workflow.
// FR-2 requires routing within 5 s; this is a few indexed queries plus one Temporal signal.
func (s *Service) HandleInbound(ctx context.Context, in gateway.Inbound) error {
	acct, err := s.Q.GetHoneypotAccount(ctx, in.AccountID)
	if err != nil {
		return fmt.Errorf("load account %d: %w", in.AccountID, err)
	}
	if acct.Status != db.AccountStatusActive {
		s.Log.Info("message to non-active honeypot account ignored", "account_id", acct.ID, "status", acct.Status)
		return nil
	}
	p, err := s.Q.GetPersona(ctx, acct.PersonaID)
	if err != nil {
		return fmt.Errorf("load persona %d: %w", acct.PersonaID, err)
	}
	if p.Status != db.PersonaStatusActive {
		s.Log.Info("message to a persona that is not active ignored", "persona_id", p.ID, "status", p.Status)
		return nil
	}

	conv, created, err := s.openConversation(ctx, acct, in)
	if err != nil {
		return err
	}

	msg, inserted, err := s.storeMessage(ctx, conv.ID, db.MessageDirectionInbound, db.SenderTypeScammer, 0, in.Text, in.SentAt, in.DedupeKey)
	if err != nil {
		return err
	}
	if !inserted {
		return nil // redelivered update
	}
	opts := client.StartWorkflowOptions{ID: conv.TemporalWorkflowID, TaskQueue: s.Cfg.TaskQueue}
	input := workflows.ConversationInput{ConversationID: conv.ID, Timing: s.Cfg.Timing, Human: conv.Status == db.ConversationStatusHuman,
		Persona: workflows.ParsePersonaTiming(p.Profile)}
	if _, err := s.Temporal.SignalWithStartWorkflow(ctx, conv.TemporalWorkflowID, workflows.SignalInbound,
		workflows.InboundSignal{MessageID: msg.ID}, opts, workflows.ConversationWorkflow, input); err != nil {
		return fmt.Errorf("signal conversation workflow: %w", err)
	}
	s.Log.Info("inbound routed", "conversation_id", conv.ID, "message_id", msg.ID, "new_conversation", created, "handle", in.Handle)
	return nil
}

func (s *Service) openConversation(ctx context.Context, acct db.HoneypotAccount, in gateway.Inbound) (db.Conversation, bool, error) {
	find := db.FindOpenConversationByPeerParams{AccountID: acct.ID, PeerBidx: s.Box.PeerRefIndex(in.PeerRef), PeerRef: in.PeerRef}
	conv, err := s.Q.FindOpenConversationByPeer(ctx, find)
	if err == nil {
		s.revealConversation(&conv)
		return conv, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.Conversation{}, false, err
	}
	// If the persona runs exactly one playbook, start with it; otherwise the classifier picks one.
	var playbookID pgtype.Int8
	if pbs, err := s.Q.ListPersonaPlaybooks(ctx, acct.PersonaID); err == nil && len(pbs) == 1 {
		playbookID = pgtype.Int8{Int64: pbs[0].ID, Valid: true}
	}
	handle := truncate(in.Handle, 255)
	sealed, err := s.sealConversationIdentifiers(acct.ID, handle, in.PeerRef)
	if err != nil {
		return db.Conversation{}, false, err
	}
	conv, err = s.Q.CreateConversationForPeer(ctx, db.CreateConversationForPeerParams{
		AccountID:                acct.ID,
		PlaybookID:               playbookID,
		TemporalWorkflowID:       fmt.Sprintf("conversation-%d-%d", acct.ID, s.Now().UnixNano()),
		ScammerHandle:            s.plaintextOrNull(handle),
		ScammerPeerRef:           s.plaintextOrNull(in.PeerRef),
		StartedAt:                in.SentAt,
		ScammerHandleCiphertext:  sealed.handle,
		ScammerPeerRefCiphertext: sealed.peerRef,
		ScammerPeerRefBidx:       sealed.peerBidx,
		IdentifiersDekRef:        sealed.dekRef,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// A concurrent first message created the open conversation (conversations_open_peer_bidx_uq).
		conv, err = s.Q.FindOpenConversationByPeer(ctx, find)
		s.revealConversation(&conv)
		return conv, false, err
	}
	if err != nil {
		return db.Conversation{}, false, err
	}
	s.revealConversation(&conv)
	// Scam phase history starts with the initial stage (best effort: routing must not fail on it).
	if _, herr := s.Q.InsertStageEvent(ctx, db.InsertStageEventParams{ConversationID: conv.ID, ToStage: conv.Stage, ScamType: conv.ScamType, Source: StageSourceBot}); herr != nil {
		s.Log.Warn("initial stage event not recorded", "conversation_id", conv.ID, "err", herr)
	}
	return conv, true, nil
}

// storeMessage encrypts and appends a message. inserted is false when dedupeKey already exists.
func (s *Service) storeMessage(ctx context.Context, convID int64, dir db.MessageDirection, sender db.SenderType, operatorID int64, text string, at time.Time, dedupeKey string) (db.Message, bool, error) {
	ct, ref, err := s.Box.Seal([]byte(text), aad(convID))
	if err != nil {
		return db.Message{}, false, err
	}
	params := db.InsertMessageDedupedParams{ConversationID: convID, Direction: dir, SenderType: sender, BodyCiphertext: ct, DekRef: ref, SentAt: at}
	if operatorID != 0 {
		params.OperatorID = pgtype.Int8{Int64: operatorID, Valid: true}
	}
	if dedupeKey != "" {
		params.DedupeKey = pgtype.Text{String: dedupeKey, Valid: true}
	}
	// InsertMessageDeduped also bumps conversations.last_message_at.
	row, err := s.Q.InsertMessageDeduped(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Message{}, false, nil
	}
	return db.Message(row), err == nil, err
}

// PlainMessage is a decrypted message for the persona engine and the operator console.
// Text is untrusted scammer content when SenderType is "scammer": render it as plain text only.
// Undecryptable messages (wrong key, corrupted ciphertext) carry UndecryptableText and
// Undecryptable=true; they are shown as such in the console and never sent to an LLM (readable()).
type PlainMessage struct {
	ID            int64     `json:"id"`
	Direction     string    `json:"direction"`
	SenderType    string    `json:"sender_type"`
	OperatorID    *int64    `json:"operator_id,omitempty"`
	Text          string    `json:"text"`
	SentAt        time.Time `json:"sent_at"`
	Undecryptable bool      `json:"undecryptable,omitempty"`
}

// UndecryptableText is the placeholder body of a message that could not be decrypted.
const UndecryptableText = "[message could not be decrypted]"

// decrypt opens one message. On failure it returns the placeholder message (Undecryptable=true)
// together with the error, so list paths can keep going and single-message paths can decide.
func (s *Service) decrypt(m db.Message) (PlainMessage, error) {
	pm := PlainMessage{ID: m.ID, Direction: string(m.Direction), SenderType: string(m.SenderType), SentAt: m.SentAt}
	if m.OperatorID.Valid {
		id := m.OperatorID.Int64
		pm.OperatorID = &id
	}
	pt, err := s.Box.Open(m.BodyCiphertext, m.DekRef, aad(m.ConversationID))
	if err != nil {
		pm.Text, pm.Undecryptable = UndecryptableText, true
		return pm, fmt.Errorf("decrypt message %d: %w", m.ID, err)
	}
	pm.Text = string(pt)
	return pm, nil
}

// History returns up to limit messages in chronological order. A message that fails to decrypt is
// logged with its id and returned as a placeholder (Undecryptable=true); it never fails the list.
func (s *Service) History(ctx context.Context, convID int64, limit int32) ([]PlainMessage, error) {
	rows, err := s.Q.ListRecentMessages(ctx, db.ListRecentMessagesParams{ConversationID: convID, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]PlainMessage, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		pm, err := s.decrypt(rows[i])
		if err != nil {
			// Once per message per process at ERROR: the console polls History every few seconds.
			lvl := slog.LevelDebug
			if _, seen := undecryptableLogged.LoadOrStore(rows[i].ID, true); !seen {
				lvl = slog.LevelError
			}
			s.Log.Log(ctx, lvl, "message could not be decrypted; shown as placeholder", "conversation_id", convID,
				"message_id", rows[i].ID, "dek_ref", rows[i].DekRef, "err", err)
		}
		out = append(out, pm)
	}
	return out, nil
}

// undecryptableLogged holds the ids of messages already reported as undecryptable (log once).
var undecryptableLogged sync.Map

// readable drops undecryptable messages: only real text may reach an LLM prompt or a classifier.
func readable(hist []PlainMessage) []PlainMessage {
	out := make([]PlainMessage, 0, len(hist))
	for _, m := range hist {
		if !m.Undecryptable {
			out = append(out, m)
		}
	}
	return out
}

// ---- operator actions (FR-5) ----

// SetControl takes over (human=true) or hands back to the bot. The database status changes first,
// so even an in-flight SendReply activity refuses to send; then the workflow is signalled.
func (s *Service) SetControl(ctx context.Context, convID int64, human bool) (db.Conversation, error) {
	conv, err := s.Q.GetConversation(ctx, convID)
	if err != nil {
		return conv, notFound(err)
	}
	if conv.Status == db.ConversationStatusClosed {
		return conv, fmt.Errorf("%w: conversation is closed", ErrConflict)
	}
	status, action := db.ConversationStatusBot, workflows.ActionRelease
	if human {
		status, action = db.ConversationStatusHuman, workflows.ActionTakeover
	}
	if _, err := s.Q.SetConversationStatus(ctx, db.SetConversationStatusParams{ID: convID, Status: status}); err != nil {
		return conv, err
	}
	conv.Status = status
	s.signal(ctx, conv, workflows.ControlSignal{Action: action})
	return conv, nil
}

func (s *Service) Close(ctx context.Context, convID int64) error {
	conv, err := s.Q.GetConversation(ctx, convID)
	if err != nil {
		return notFound(err)
	}
	if _, err := s.Q.SetConversationStatus(ctx, db.SetConversationStatusParams{ID: convID, Status: db.ConversationStatusClosed}); err != nil {
		return err
	}
	s.signal(ctx, conv, workflows.ControlSignal{Action: workflows.ActionClose})
	return nil
}

func (s *Service) signal(ctx context.Context, conv db.Conversation, sig workflows.ControlSignal) {
	err := s.Temporal.SignalWorkflow(ctx, conv.TemporalWorkflowID, "", workflows.SignalControl, sig)
	var nf *serviceerror.NotFound
	if err != nil && !errors.As(err, &nf) {
		s.Log.Error("control signal failed", "conversation_id", conv.ID, "action", sig.Action, "err", err)
	}
}

// OperatorSend sends a message as the persona after takeover. It goes through the same gateway
// as bot replies (TR-5) and is stored with sender_type=operator and the operator's id.
func (s *Service) OperatorSend(ctx context.Context, convID, operatorID int64, text string) (PlainMessage, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 4000 {
		return PlainMessage{}, fmt.Errorf("%w: text must be 1-4000 characters", ErrBadRequest)
	}
	conv, err := s.conversation(ctx, convID)
	if err != nil {
		return PlainMessage{}, notFound(err)
	}
	if conv.Status != db.ConversationStatusHuman {
		return PlainMessage{}, fmt.Errorf("%w: take over the conversation before sending", ErrConflict)
	}
	acct, err := s.Q.GetHoneypotAccount(ctx, conv.AccountID)
	if err != nil {
		return PlainMessage{}, err
	}
	sender, err := s.SenderFor(acct)
	if err != nil {
		return PlainMessage{}, err
	}
	rid := randomInt63()
	if !conv.ScammerPeerRef.Valid {
		return PlainMessage{}, fmt.Errorf("conversation %d: scammer reply address is unavailable (not decryptable)", conv.ID)
	}
	if err := sender.Send(ctx, acct.ID, conv.ScammerPeerRef.String, text, rid); err != nil {
		return PlainMessage{}, err
	}
	m, _, err := s.storeMessage(ctx, conv.ID, db.MessageDirectionOutbound, db.SenderTypeOperator, operatorID, text, s.Now(),
		fmt.Sprintf("out:%d:%d", conv.ID, rid))
	if err != nil {
		return PlainMessage{}, err
	}
	pm, _ := s.decrypt(m) // just sealed with the same key; a failure would show as the placeholder
	return pm, nil
}

// SenderFor picks the outbound sender for an account: the simulator for "sim:" accounts,
// otherwise the connector registered for the account's channel.
func (s *Service) SenderFor(acct db.HoneypotAccount) (gateway.Sender, error) {
	if sim.IsSimAccount(acct.SessionRef) {
		if s.Sim == nil {
			return nil, fmt.Errorf("account %d is simulated but the simulator is disabled (SOCKABUS_SIM=1)", acct.ID)
		}
		return s.Sim, nil
	}
	return s.Senders.For(string(acct.Channel))
}

func randomInt63() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]) &^ (1 << 63))
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
