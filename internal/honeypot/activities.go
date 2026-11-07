package honeypot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"

	"github.com/JaxzTan/Buru_AI/internal/db"
	"github.com/JaxzTan/Buru_AI/internal/extraction"
	"github.com/JaxzTan/Buru_AI/internal/gateway"
	"github.com/JaxzTan/Buru_AI/internal/llm"
	"github.com/JaxzTan/Buru_AI/internal/persona"
	"github.com/JaxzTan/Buru_AI/internal/playbook"
	"github.com/JaxzTan/Buru_AI/internal/workflows"
)

// LLM time limits. Extraction and classification fall back to regex and keywords; the persona
// falls back to the playbook's example line. Override with SOCKABUS_LLM_TIMEOUT (persona replies).
var (
	helperLLMTimeout  = 20 * time.Second
	personaLLMTimeout = 45 * time.Second
)

// SetPersonaLLMTimeout changes the persona reply time limit (from config).
func SetPersonaLLMTimeout(d time.Duration) {
	if d > 0 {
		personaLLMTimeout = d
	}
}

// Activities are the side effects of ConversationWorkflow.
type Activities struct{ S *Service }

// Register adds the activities to a worker under the names the workflow calls.
func (a *Activities) Register(w worker.Worker) {
	reg := func(fn any, name string) { w.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name}) }
	reg(a.ProcessInbound, workflows.ActProcessInbound)
	reg(a.MarkRead, workflows.ActMarkRead)
	reg(a.GenerateReply, workflows.ActGenerateReply)
	reg(a.SimulateTyping, workflows.ActSimulateTyping)
	reg(a.SendReply, workflows.ActSendReply)
	reg(a.CloseConversation, workflows.ActCloseConversation)
}

// ProcessInbound extracts entities (FR-6, TR-6), advances the playbook stage (FR-4, TR-4) and
// re-classifies the scam type when needed (FR-7, TR-7).
func (a *Activities) ProcessInbound(ctx context.Context, in workflows.ProcessInboundInput) (workflows.ProcessInboundResult, error) {
	s := a.S
	m, err := s.Q.GetMessage(ctx, in.MessageID)
	if err != nil {
		return workflows.ProcessInboundResult{}, err
	}
	conv, err := s.conversation(ctx, in.ConversationID)
	if err != nil {
		return workflows.ProcessInboundResult{}, err
	}
	pm, err := s.decrypt(m)
	if err != nil {
		// Never retried and never sent to the extraction LLM: the text cannot be recovered. The
		// conversation keeps its stage; the console shows the message as undecryptable.
		s.Log.Error("inbound message could not be decrypted; skipping extraction", "conversation_id", conv.ID, "message_id", m.ID, "err", err)
		scam := ""
		if conv.ScamType.Valid {
			scam = string(conv.ScamType.ScamType)
		}
		return workflows.ProcessInboundResult{Stage: conv.Stage, ScamType: scam}, nil
	}
	acct, err := s.Q.GetHoneypotAccount(ctx, conv.AccountID)
	if err != nil {
		return workflows.ProcessInboundResult{}, err
	}

	xctx, cancel := context.WithTimeout(ctx, helperLLMTimeout)
	found, xerr := extraction.ExtractWithLLM(xctx, s.ExtractLLM, pm.Text)
	cancel()
	if xerr != nil {
		s.Log.Warn("LLM extraction failed; using regex results", "conversation_id", conv.ID, "err", xerr)
	}
	count, err := s.Q.CountInboundMessages(ctx, conv.ID)
	if err != nil {
		return workflows.ProcessInboundResult{}, err
	}

	firstID, err := s.Q.FirstInboundMessageID(ctx, conv.ID)
	if err != nil {
		return workflows.ProcessInboundResult{}, err
	}

	newEntities := 0
	paymentDetails := false
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := s.Q.WithTx(tx)
		if m.ID == firstID { // the scammer's own handle is intelligence too (FR-6), once per conversation
			if v, ok := extraction.NormalizeValue("handle", conv.ScammerHandle.String); conv.ScammerHandle.Valid && ok {
				isNew, err := recordEntity(ctx, q, "handle", v, extraction.BankOrChain("handle", string(acct.Channel)), "low", conv.ID, m.ID)
				if err != nil {
					return err
				}
				if isNew {
					newEntities++
				}
			}
		}
		for _, f := range found {
			if f.Type == "bank_account" || f.Type == "wallet" {
				paymentDetails = true
			}
			isNew, err := recordEntity(ctx, q, f.Type, f.Value, extraction.BankOrChain(f.Type, f.Bank), extraction.Level(f.Confidence), conv.ID, m.ID)
			if err != nil {
				return err
			}
			if isNew {
				newEntities++
			}
		}
		return nil
	})
	if err != nil {
		return workflows.ProcessInboundResult{}, fmt.Errorf("store entities: %w", err)
	}

	next := playbook.Next(conv.Stage, playbook.Signals{
		InboundCount:        int(count),
		PaymentAsked:        playbook.AsksForPayment(pm.Text),
		PaymentDetailsFound: paymentDetails,
	})
	stageChanged := next != conv.Stage
	if stageChanged {
		if err := s.setStage(ctx, conv, next, StageSourceBot); err != nil {
			return workflows.ProcessInboundResult{}, err
		}
		conv.Stage = next
	}

	scam := ""
	if conv.ScamType.Valid {
		scam = string(conv.ScamType.ScamType)
	}
	if scam == "" || scam == "other" || stageChanged {
		label, err := a.classify(ctx, conv.ID)
		if err != nil {
			return workflows.ProcessInboundResult{}, err
		}
		if label != scam {
			if err := a.setScamType(ctx, conv, acct.PersonaID, label); err != nil {
				return workflows.ProcessInboundResult{}, err
			}
			scam = label
		}
	}
	// AI summary refresh (TR-7): on every stage change and every SummaryEveryN scammer messages.
	// Runs in the background so it never delays the reply; no workflow change (no replay risk).
	switch {
	case stageChanged:
		s.RefreshConversationSummaryAsync(conv.ID, "stage_change")
	case count > 0 && count%SummaryEveryN == 0:
		s.RefreshConversationSummaryAsync(conv.ID, "every_n")
	}
	return workflows.ProcessInboundResult{Stage: next, ScamType: scam, NewEntities: newEntities,
		Chars: utf8.RuneCountInString(pm.Text), Question: strings.Contains(pm.Text, "?")}, nil
}

// recordEntity upserts an entity and appends a sighting (which also bumps sightings_count).
// It returns true when the sighting is new.
func recordEntity(ctx context.Context, q *db.Queries, typ, value, bankOrChain, level string, convID, msgID int64) (bool, error) {
	et := db.EntityType(typ)
	if !et.Valid() {
		return false, nil
	}
	if level != "low" && level != "medium" {
		level = "low" // extraction can never set "high" (FR-9)
	}
	bankOrChain = extraction.BankOrChain(typ, bankOrChain) // idempotent; guarantees UPPER CASE on every write
	boc := pgtype.Text{String: bankOrChain, Valid: bankOrChain != ""}
	e, err := q.UpsertEntity(ctx, db.UpsertEntityParams{Type: et, ValueNormalized: value, BankOrChain: boc, Confidence: db.ConfidenceLevel(level)})
	if err != nil {
		return false, err
	}
	_, err = q.InsertSighting(ctx, db.InsertSightingParams{EntityID: e.ID, ConversationID: convID, MessageID: msgID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // already recorded for this message (activity retry)
	}
	return err == nil, err
}

func (a *Activities) classify(ctx context.Context, convID int64) (string, error) {
	hist, err := a.S.History(ctx, convID, 60)
	if err != nil {
		return "", err
	}
	var scammer []string
	for _, m := range readable(hist) {
		if m.SenderType == string(db.SenderTypeScammer) {
			scammer = append(scammer, m.Text)
		}
	}
	cctx, cancel := context.WithTimeout(ctx, helperLLMTimeout)
	defer cancel()
	return extraction.ClassifyWithLLM(cctx, a.S.ExtractLLM, strings.Join(scammer, "\n")), nil
}

// setScamType stores the label and switches to the matching playbook: the persona's own playbook
// for that scam type if it has one, else the latest playbook for the type, else keep the current one.
func (a *Activities) setScamType(ctx context.Context, conv db.Conversation, personaID int64, label string) error {
	q := a.S.Q
	pb := conv.PlaybookID
	if pbs, err := q.ListPersonaPlaybooks(ctx, personaID); err == nil {
		for _, p := range pbs {
			if string(p.ScamType) == label {
				pb = pgtype.Int8{Int64: p.ID, Valid: true}
				break
			}
		}
	}
	if pb == conv.PlaybookID && label != "other" {
		if p, err := q.PlaybookForScamType(ctx, db.ScamType(label)); err == nil {
			pb = pgtype.Int8{Int64: p.ID, Valid: true}
		}
	}
	if err := a.S.setScamTypeRow(ctx, conv, db.NullScamType{ScamType: db.ScamType(label), Valid: true}, StageSourceClassifier); err != nil {
		return err
	}
	if pb == conv.PlaybookID {
		return nil
	}
	return q.SetConversationPlaybook(ctx, db.SetConversationPlaybookParams{ID: conv.ID, PlaybookID: pb})
}

// MarkRead shows the scammer's messages as read. Best effort: a failure must not delay the reply.
func (a *Activities) MarkRead(ctx context.Context, in workflows.ConversationRef) error {
	conv, acct, sender, err := a.target(ctx, in.ConversationID)
	if err != nil || a.S.repliesPaused(acct) {
		return nil
	}
	if err := sender.MarkRead(ctx, acct.ID, conv.ScammerPeerRef.String); err != nil {
		a.S.Log.Warn("mark read failed", "conversation_id", conv.ID, "err", err)
	}
	return nil
}

// GenerateReply builds the persona prompt and asks the LLM for the next reply (FR-1, FR-4). The
// model answers {"action","bubbles"}: reply, ignore (never twice in a row) or brb; the workflow
// owns the timing. Bubbles get the persona's typo rate, then pass the output guard again.
func (a *Activities) GenerateReply(ctx context.Context, in workflows.GenerateReplyInput) (workflows.GenerateReplyResult, error) {
	s := a.S
	conv, err := s.Q.GetConversation(ctx, in.ConversationID)
	if err != nil {
		return workflows.GenerateReplyResult{}, err
	}
	if conv.Status != db.ConversationStatusBot {
		return workflows.GenerateReplyResult{Source: "none"}, nil
	}
	acct, err := s.Q.GetHoneypotAccount(ctx, conv.AccountID)
	if err != nil {
		return workflows.GenerateReplyResult{}, err
	}
	if s.repliesPaused(acct) {
		s.Log.Info("telegram replies are off (SOCKABUS_TG_REPLIES=0): no reply generated", "conversation_id", conv.ID)
		return workflows.GenerateReplyResult{Source: "none"}, nil
	}
	p, err := s.Q.GetPersona(ctx, acct.PersonaID)
	if err != nil {
		return workflows.GenerateReplyResult{}, err
	}
	hist, err := s.History(ctx, conv.ID, 40)
	if err != nil {
		return workflows.GenerateReplyResult{}, err
	}
	hist = readable(hist) // undecryptable messages never reach the persona LLM
	if len(hist) == 0 || hist[len(hist)-1].Direction != string(db.MessageDirectionInbound) {
		return workflows.GenerateReplyResult{Source: "none"}, nil // nothing new to answer
	}

	stage := playbook.Generic(conv.Stage)
	if conv.PlaybookID.Valid {
		if pb, err := s.Q.GetPlaybook(ctx, conv.PlaybookID.Int64); err == nil {
			if stages, err := playbook.ParseStages(pb.Stages); err == nil && len(stages) > 0 {
				stage = playbook.Find(stages, conv.Stage)
			}
		}
	}

	if s.PersonaLLM == nil {
		return fallbackReply(stage, hist), nil
	}

	prof := persona.Profile{Name: p.Name, Age: int(p.Age.Int16), LanguageMix: p.LanguageMix, Backstory: p.Backstory.String, Wealth: p.WealthProfile}
	var pf struct {
		Ethnicity string `json:"ethnicity"`
	}
	if json.Unmarshal(p.Profile, &pf) == nil {
		prof.Ethnicity = pf.Ethnicity
	}
	if pv, err := s.Q.LatestPromptVersion(ctx, p.ID); err == nil {
		prof.SystemPrompt = pv.SystemPrompt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return workflows.GenerateReplyResult{}, err
	}
	scam := ""
	if conv.ScamType.Valid {
		scam = string(conv.ScamType.ScamType)
	}
	var got []persona.Extracted
	if ents, err := s.Q.ListConversationEntities(ctx, conv.ID); err != nil {
		s.Log.Warn("listing extracted entities failed; persona replies without them", "conversation_id", conv.ID, "err", err)
	} else {
		for _, e := range ents {
			if e.Status != db.EntityStatusRejected {
				got = append(got, persona.Extracted{Type: string(e.Type), BankOrChain: e.BankOrChain.String, Value: e.ValueNormalized})
			}
		}
	}
	sit := persona.Situation{Channel: string(acct.Channel), ScamType: scam, Stage: stage, Now: s.Now(), Got: got,
		NoIgnore: in.NoIgnore, WasAway: in.WasAway}
	// How long the scammer has waited: since their first message after the persona's last one.
	for i := len(hist) - 1; i >= 0 && hist[i].Direction == string(db.MessageDirectionInbound); i-- {
		if !hist[i].SentAt.IsZero() {
			sit.LateBy = s.Now().Sub(hist[i].SentAt)
		}
	}
	system := persona.SystemPrompt(prof, sit)
	typoRate := s.personaTiming(p).TypoRate

	turns := make([]llm.Turn, 0, len(hist))
	for _, m := range hist {
		role := llm.RoleModel
		if m.Direction == string(db.MessageDirectionInbound) {
			role = llm.RoleUser // scammer text stays in user turns only (TRD §7)
		}
		turns = append(turns, llm.Turn{Role: role, Text: m.Text})
	}

	retryNote := "(Your previous draft broke the rules above. Write a new reply, fully in character.)"
	for try := 0; try < 2; try++ {
		sys := system
		if try > 0 {
			sys += "\n\n" + retryNote
		}
		gctx, cancel := context.WithTimeout(ctx, personaLLMTimeout)
		out, err := s.PersonaLLM.Generate(gctx, llm.Request{System: sys, Turns: turns, MaxOutputTokens: 2048, Effort: llm.EffortMedium})
		timedOut := gctx.Err() != nil
		cancel()
		if err != nil {
			// Retry quick failures (HTTP 5xx, 429). A blocked reply or a hung call falls back to the
			// playbook line at once, so a slow LLM never leaves the scammer waiting for minutes.
			if !errors.Is(err, llm.ErrBlocked) && !timedOut && activity.GetInfo(ctx).Attempt < 3 {
				return workflows.GenerateReplyResult{}, err // transient: let Temporal retry
			}
			s.Log.Warn("persona LLM unavailable; using playbook line", "conversation_id", conv.ID, "err", err)
			return fallbackReply(stage, hist), nil
		}
		action, text := persona.ParseReply(out)
		switch {
		case action == persona.ActionIgnore && in.NoIgnore:
			retryNote = "(You already left their last message on read. Answer this time, fully in character.)"
			continue
		case action == persona.ActionIgnore || (action == persona.ActionBrb && strings.TrimSpace(text) == ""):
			return workflows.GenerateReplyResult{Source: "llm", Action: action}, nil
		}
		if bubbles, ok := persona.Guard(p.Name, text); ok {
			return workflows.GenerateReplyResult{Bubbles: withTypos(p.Name, bubbles, typoRate), Source: "llm", Action: action}, nil
		}
		s.Log.Warn("persona reply rejected by output guard", "conversation_id", conv.ID, "try", try+1)
	}
	return fallbackReply(stage, hist), nil
}

// withTypos injects the persona's fat-finger typos and re-checks every bubble with the output
// guard; if any bubble fails, the clean bubbles are sent instead.
func withTypos(name string, bubbles []string, rate float64) []string {
	typed := persona.AddTypos(bubbles, rate, rand.New(rand.NewSource(time.Now().UnixNano())))
	for _, b := range typed {
		if g, ok := persona.Guard(name, b); !ok || len(g) != 1 || g[0] != b {
			return bubbles
		}
	}
	return typed
}

var fillers = []string{"hmm ok..", "sorry just saw ur msg", "can explain again? i dont really understand", "wait ah, my phone very slow"}

// fallbackReply uses the stage's example line, or a filler if the persona already said it.
func fallbackReply(stage playbook.Stage, hist []PlainMessage) workflows.GenerateReplyResult {
	said := map[string]bool{}
	outbound := 0
	for _, m := range hist {
		if m.Direction == string(db.MessageDirectionOutbound) {
			said[m.Text] = true
			outbound++
		}
	}
	line := stage.Example
	if line == "" || said[line] {
		line = fillers[outbound%len(fillers)]
	}
	return workflows.GenerateReplyResult{Bubbles: []string{line}, Source: "fallback"}
}

// SimulateTyping keeps "typing…" visible for the given duration (FR-3). Telegram's typing status
// expires after ~5 s, so it is refreshed. Cancelled at once on takeover.
func (a *Activities) SimulateTyping(ctx context.Context, in workflows.SimulateTypingInput) error {
	conv, acct, sender, err := a.target(ctx, in.ConversationID)
	if err != nil {
		return err
	}
	if a.S.repliesPaused(acct) {
		return nil
	}
	peer := conv.ScammerPeerRef.String
	if err := sender.SetOnline(ctx, acct.ID, true); err != nil {
		a.S.Log.Warn("set online failed", "account_id", acct.ID, "err", err)
	}
	deadline := time.Now().Add(in.Duration)
	for {
		if err := sender.Typing(ctx, acct.ID, peer); err != nil {
			a.S.Log.Warn("typing indicator failed", "conversation_id", conv.ID, "err", err)
		}
		activity.RecordHeartbeat(ctx)
		left := time.Until(deadline)
		if left <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(4500*time.Millisecond, left)):
		}
	}
}

// SendReply sends one bubble as the persona and appends it to messages. It re-checks the database
// status first, so a takeover that lands mid-reply still stops the bot (FR-5).
func (a *Activities) SendReply(ctx context.Context, in workflows.SendReplyInput) (workflows.SendReplyResult, error) {
	conv, acct, sender, err := a.target(ctx, in.ConversationID)
	if err != nil {
		return workflows.SendReplyResult{}, err
	}
	if conv.Status != db.ConversationStatusBot {
		return workflows.SendReplyResult{Sent: false, Reason: "conversation is " + string(conv.Status)}, nil
	}
	if a.S.repliesPaused(acct) {
		return workflows.SendReplyResult{Sent: false, Reason: "telegram replies are off (SOCKABUS_TG_REPLIES=0)"}, nil
	}
	if err := sender.Send(ctx, acct.ID, conv.ScammerPeerRef.String, in.Text, in.RandomID); err != nil {
		return workflows.SendReplyResult{}, err
	}
	if _, _, err := a.S.storeMessage(ctx, conv.ID, db.MessageDirectionOutbound, db.SenderTypePersona, 0, in.Text, a.S.Now(),
		fmt.Sprintf("out:%d:%d", conv.ID, in.RandomID)); err != nil {
		return workflows.SendReplyResult{}, err
	}
	if err := sender.SetOnline(ctx, acct.ID, false); err != nil {
		a.S.Log.Debug("set offline failed", "account_id", acct.ID, "err", err)
	}
	return workflows.SendReplyResult{Sent: true}, nil
}

func (a *Activities) CloseConversation(ctx context.Context, in workflows.CloseInput) error {
	a.S.Log.Info("closing conversation", "conversation_id", in.ConversationID, "reason", in.Reason)
	_, err := a.S.Q.SetConversationStatus(ctx, db.SetConversationStatusParams{ID: in.ConversationID, Status: db.ConversationStatusClosed})
	return err
}

func (a *Activities) target(ctx context.Context, convID int64) (db.Conversation, db.HoneypotAccount, gateway.Sender, error) {
	conv, err := a.S.conversation(ctx, convID)
	if err != nil {
		return conv, db.HoneypotAccount{}, nil, err
	}
	if !conv.ScammerPeerRef.Valid {
		return conv, db.HoneypotAccount{}, nil, fmt.Errorf("conversation %d: scammer reply address is unavailable (not decryptable)", conv.ID)
	}
	acct, err := a.S.Q.GetHoneypotAccount(ctx, conv.AccountID)
	if err != nil {
		return conv, acct, nil, err
	}
	sender, err := a.S.SenderFor(acct)
	return conv, acct, sender, err
}
