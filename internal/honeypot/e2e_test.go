package honeypot

// End-to-end test of the live honeypot loop against real PostgreSQL and a real Temporal server,
// with a fake channel sender and a fake LLM:
//
//	SOCKABUS_TEST_POSTGRES_URL=postgres://buru:buru@localhost:15432/sockabus_e2e?sslmode=disable \
//	SOCKABUS_TEST_TEMPORAL=localhost:7233 go test ./internal/honeypot -run TestE2E -v
//
// Start the scratch DB first (make e2e-up: migrated + seeded). Skipped when the env vars are not set.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/JaxzTan/Buru_AI/internal/db"
	"github.com/JaxzTan/Buru_AI/internal/gateway"
	"github.com/JaxzTan/Buru_AI/internal/llm"
	"github.com/JaxzTan/Buru_AI/internal/msgcrypt"
	"github.com/JaxzTan/Buru_AI/internal/workflows"
)

type sentMsg struct {
	peer string
	text string
	at   time.Time
}

type fakeSender struct {
	mu     sync.Mutex
	sent   []sentMsg
	typing int
	reads  int
}

func (f *fakeSender) Send(_ context.Context, _ int64, peer, text string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMsg{peer, text, time.Now()})
	return nil
}
func (f *fakeSender) Typing(context.Context, int64, string) error {
	f.mu.Lock()
	f.typing++
	f.mu.Unlock()
	return nil
}
func (f *fakeSender) MarkRead(context.Context, int64, string) error {
	f.mu.Lock()
	f.reads++
	f.mu.Unlock()
	return nil
}
func (f *fakeSender) SetOnline(context.Context, int64, bool) error { return nil }
func (f *fakeSender) count() int                                   { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sent) }

// fakeLLM answers persona, extraction and classification prompts.
type fakeLLM struct {
	mu      sync.Mutex
	systems []string
	turns   [][]llm.Turn
}

func (f *fakeLLM) Name() string { return "fake" }
func (f *fakeLLM) Generate(_ context.Context, r llm.Request) (string, error) {
	f.mu.Lock()
	f.systems = append(f.systems, r.System)
	f.turns = append(f.turns, r.Turns)
	f.mu.Unlock()
	switch {
	case strings.Contains(r.System, "extract scam intelligence"):
		return `{"entities":[]}`, nil
	case strings.Contains(r.System, "Classify a scam conversation"):
		return `{"label":"investment"}`, nil
	default:
		return "wah 8% so high ah..\n\nwhich account i send?", nil
	}
}

func TestE2ETelegramHoneypotLoop(t *testing.T) {
	pgURL, temporalAddr := os.Getenv("SOCKABUS_TEST_POSTGRES_URL"), os.Getenv("SOCKABUS_TEST_TEMPORAL")
	if pgURL == "" || temporalAddr == "" {
		t.Skip("set SOCKABUS_TEST_POSTGRES_URL and SOCKABUS_TEST_TEMPORAL to run the end-to-end test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tc, err := client.Dial(client.Options{HostPort: temporalAddr})
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()

	box, _ := msgcrypt.New(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	sender := &fakeSender{}
	senders := gateway.NewRegistry()
	senders.Register(gateway.ChannelTelegram, sender)
	fake := &fakeLLM{}
	queue := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	// ReadMin below 10 s on purpose: the workflow must clamp it to the FR-3 floor.
	timing := workflows.Timing{ReadMin: time.Second, ReadMax: 11 * time.Second, TypingMin: time.Second, TypingMax: time.Second, BubbleGap: time.Millisecond}
	svc := NewService(pool, box, fake, fake, tc, senders, Config{TaskQueue: queue, Timing: timing}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	w := worker.New(tc, queue, worker.Options{})
	workflows.Register(w)
	(&Activities{S: svc}).Register(w)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Persona + account, unique per run.
	suffix := time.Now().UnixNano()
	var opID int64
	if err := pool.QueryRow(ctx, `INSERT INTO operators (email, totp_secret_ref) VALUES ($1, 'unset') RETURNING id`, fmt.Sprintf("e2e-%d@test", suffix)).Scan(&opID); err != nil {
		t.Fatal(err)
	}
	p, err := svc.CreatePersona(ctx, PersonaInput{Name: fmt.Sprintf("E2E Auntie %d", suffix), Age: 62, Status: "active",
		LanguageMix: []byte(`{"en":0.6,"nan":0.4}`), Backstory: "Retired, Penang.", SystemPrompt: "You are E2E Auntie, 62, Penang."}, opID)
	if err != nil {
		t.Fatal(err)
	}
	acct, err := svc.BindAccount(ctx, AccountInput{PersonaID: p.ID, Channel: "telegram", Phone: fmt.Sprintf("+60%d", suffix%1e10), SessionRef: "file:e2e.json"})
	if err != nil {
		t.Fatal(err)
	}

	peer := fmt.Sprintf("tg:user:%d:42", suffix)
	inbound := func(id int, text string) gateway.Inbound {
		return gateway.Inbound{Channel: "telegram", AccountID: acct.ID, PeerRef: peer, Handle: "@gold_usdt_mentor",
			ChannelMsgID: fmt.Sprint(id), Text: text, SentAt: time.Now().UTC(), DedupeKey: fmt.Sprintf("tg:%d:%d:%d", acct.ID, suffix, id)}
	}

	// 1. First scammer message → one conversation, persona replies after >= 10 s with typing shown.
	start := time.Now()
	if err := svc.HandleInbound(ctx, inbound(1, "Good morning 🌸 our USDT plan give 8% weekly, my student withdraw 3 times")); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleInbound(ctx, inbound(1, "duplicate redelivery")); err != nil { // same dedupe key
		t.Fatal(err)
	}
	waitFor(t, ctx, func() bool { return sender.count() >= 2 }, "persona reply (2 bubbles)")
	if gap := sender.sent[0].at.Sub(start); gap < workflows.MinReplyDelay {
		t.Fatalf("first reply after %v; FR-3 requires >= 10s", gap)
	}
	if sender.sent[0].text != "wah 8% so high ah.." || sender.sent[1].text != "which account i send?" || sender.sent[0].peer != peer {
		t.Fatalf("unexpected sends: %+v", sender.sent)
	}
	if sender.typing == 0 || sender.reads == 0 {
		t.Errorf("expected typing indicator and read receipt, typing=%d reads=%d", sender.typing, sender.reads)
	}

	convs, err := svc.Q.ListConversationsWithPersona(ctx, db.ListConversationsWithPersonaParams{RowLimit: 500})
	if err != nil {
		t.Fatal(err)
	}
	var convID int64
	for _, c := range convs {
		if c.AccountID == acct.ID {
			if convID != 0 {
				t.Fatal("more than one conversation for one scammer")
			}
			convID = c.ID
		}
	}
	detail, err := svc.Conversation(ctx, convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 3 || detail.Messages[0].SenderType != "scammer" || detail.Messages[2].SenderType != "persona" {
		t.Fatalf("messages = %+v (duplicate must be ignored)", detail.Messages)
	}
	if detail.Conversation.ScamType.ScamType != db.ScamTypeInvestment {
		t.Errorf("scam type = %v, want investment", detail.Conversation.ScamType)
	}
	var raw []byte
	_ = pool.QueryRow(ctx, `SELECT body_ciphertext FROM messages WHERE conversation_id = $1 ORDER BY id LIMIT 1`, convID).Scan(&raw)
	if bytes.Contains(raw, []byte("USDT")) {
		t.Fatal("message body stored in plaintext")
	}
	// Prompt-injection boundary: scammer text only in user turns, never in the system prompt.
	for _, sys := range fake.systems {
		if strings.Contains(sys, "my student withdraw") {
			t.Fatal("scammer text leaked into a system prompt")
		}
	}

	// 2. Scammer shares a wallet → entity + sighting, stage jumps to stall.
	addr := newTronAddress()
	if err := svc.HandleInbound(ctx, inbound(2, "Send to "+addr+" TRC20 only")); err != nil {
		t.Fatal(err)
	}
	// 3. Operator takes over right away: the bot must not reply to message 2.
	time.Sleep(2 * time.Second)
	if _, err := svc.SetControl(ctx, convID, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Second)
	if n := sender.count(); n != 2 {
		t.Fatalf("bot sent %d messages after takeover, want none (FR-5)", n-2)
	}
	detail, _ = svc.Conversation(ctx, convID)
	if detail.Conversation.Stage != "stall" {
		t.Errorf("stage = %s, want stall after payment details", detail.Conversation.Stage)
	}
	var wallet db.Entity
	for _, e := range detail.Entities {
		if e.Type == db.EntityTypeWallet {
			wallet = e
		}
	}
	if wallet.ID == 0 || wallet.BankOrChain.String != "TRON" || wallet.Confidence == db.ConfidenceLevelHigh {
		t.Fatalf("wallet entity = %+v (must exist, chain tron, not high before verification)", wallet)
	}

	// 4. Operator message goes out through the same gateway and is stored as operator.
	if _, err := svc.OperatorSend(ctx, convID, opID, "ok wait ah my nephew help me"); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 3 {
		t.Fatalf("operator message not sent")
	}

	// 5. Lookup policy A: an unverified wallet never matches; verified → high (FR-9).
	res, err := svc.Lookup(ctx, LookupRequest{Type: "wallet", Value: addr, Bank: "TRC20"})
	if err != nil || res.Match {
		t.Fatalf("lookup before verification = %+v, %v (want no match)", res, err)
	}
	if _, err := svc.Verify(ctx, wallet.ID, opID, "verified", "e2e"); err != nil {
		t.Fatal(err)
	}
	res, _ = svc.Lookup(ctx, LookupRequest{Type: "wallet", Value: addr})
	if !res.Match || res.Status != "verified" || res.Confidence != "high" || len(res.ScamTypes) == 0 || res.ScamTypes[0] != "investment" {
		t.Fatalf("lookup after verification = %+v", res)
	}
	if res.RiskScore < 0.85 || res.Reason != "ACCOUNT_SUPPLIED_BY_SCAMMER" || res.RecommendedAction != "HOLD_FOR_REVIEW" || res.Evidence != nil {
		t.Fatalf("lookup risk fields = %+v (want hold, no evidence without WithEvidence)", res)
	}
	res, _ = svc.Lookup(ctx, LookupRequest{Type: "wallet", Value: addr, WithEvidence: true})
	if res.Evidence == nil || res.Evidence.TranscriptRef != fmt.Sprintf("conv_%d", convID) || res.Evidence.Excerpt == "" {
		t.Fatalf("lookup evidence = %+v", res.Evidence)
	}

	// 6. Hand back to the bot. The operator already answered message 2, so the bot stays quiet
	// until the scammer writes again, then replies as the persona.
	if _, err := svc.SetControl(ctx, convID, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(12 * time.Second)
	if n := sender.count(); n != 3 {
		t.Fatalf("bot re-answered a message the operator already handled (%d sends)", n)
	}
	if err := svc.HandleInbound(ctx, inbound(3, "ok quickly, slot closing today")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ctx, func() bool { return sender.count() >= 4 }, "bot reply after release")

	if err := svc.Close(ctx, convID); err != nil {
		t.Fatal(err)
	}
	// Leave a dev database tidy: retire the test persona and its account.
	_ = svc.SetPersonaStatus(ctx, p.ID, "retired")
	_ = svc.SetAccountStatus(ctx, acct.ID, "banned", nil)
}

func waitFor(t *testing.T, ctx context.Context, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(250 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// newTronAddress returns a random, checksum-valid TRON address so each run gets a fresh entity.
func newTronAddress() string {
	payload := make([]byte, 21)
	payload[0] = 0x41
	_, _ = rand.Read(payload[1:])
	h := sha256.Sum256(payload)
	h = sha256.Sum256(h[:])
	full := append(payload, h[:4]...)
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	n := new(big.Int).SetBytes(full)
	var out []byte
	mod := new(big.Int)
	for n.Sign() > 0 {
		n.DivMod(n, big.NewInt(58), mod)
		out = append([]byte{alphabet[mod.Int64()]}, out...)
	}
	return string(out)
}

// hangingLLM never answers until its context is cancelled, like an unreachable LLM endpoint.
type hangingLLM struct{}

func (hangingLLM) Name() string { return "hanging" }
func (hangingLLM) Generate(ctx context.Context, _ llm.Request) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func TestE2EHungLLMStillReplies(t *testing.T) {
	pgURL, temporalAddr := os.Getenv("SOCKABUS_TEST_POSTGRES_URL"), os.Getenv("SOCKABUS_TEST_TEMPORAL")
	if pgURL == "" || temporalAddr == "" {
		t.Skip("set SOCKABUS_TEST_POSTGRES_URL and SOCKABUS_TEST_TEMPORAL to run the end-to-end test")
	}
	oldHelper, oldPersona := helperLLMTimeout, personaLLMTimeout
	helperLLMTimeout, personaLLMTimeout = 2*time.Second, 3*time.Second
	defer func() { helperLLMTimeout, personaLLMTimeout = oldHelper, oldPersona }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tc, err := client.Dial(client.Options{HostPort: temporalAddr})
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()

	box, _ := msgcrypt.New(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	sender := &fakeSender{}
	senders := gateway.NewRegistry()
	senders.Register(gateway.ChannelTelegram, sender)
	queue := fmt.Sprintf("e2e-hung-%d", time.Now().UnixNano())
	timing := workflows.Timing{ReadMin: time.Second, ReadMax: 11 * time.Second, TypingMin: time.Second, TypingMax: time.Second, BubbleGap: time.Millisecond}
	svc := NewService(pool, box, hangingLLM{}, hangingLLM{}, tc, senders, Config{TaskQueue: queue, Timing: timing},
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	w := worker.New(tc, queue, worker.Options{})
	workflows.Register(w)
	(&Activities{S: svc}).Register(w)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	suffix := time.Now().UnixNano()
	p, err := svc.CreatePersona(ctx, PersonaInput{Name: fmt.Sprintf("Hung Auntie %d", suffix), Age: 62, Status: "active",
		LanguageMix: []byte(`{"en":1}`), ScamTypes: []string{"investment"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	acct, err := svc.BindAccount(ctx, AccountInput{PersonaID: p.ID, Channel: "telegram", Phone: fmt.Sprintf("+61%d", suffix%1e10), SessionRef: "file:hung.json"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := svc.HandleInbound(ctx, gateway.Inbound{Channel: "telegram", AccountID: acct.ID, PeerRef: fmt.Sprintf("tg:user:%d:1", suffix),
		Handle: "@hung_test", Text: "Good morning auntie, our USDT plan give 8% weekly", SentAt: time.Now().UTC(),
		DedupeKey: fmt.Sprintf("hung:%d", suffix)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ctx, func() bool { return sender.count() >= 1 }, "fallback reply with a hung LLM")
	elapsed := time.Since(start)
	if elapsed > 40*time.Second {
		t.Fatalf("reply took %v with a hung LLM; it should fall back within the time limits", elapsed)
	}
	if got := sender.sent[0].text; got != "my FD only give 2.5%, my friend say crypto better" {
		t.Fatalf("expected the investment rapport playbook line, got %q", got)
	}
	t.Logf("hung LLM: fallback reply after %v", elapsed.Round(time.Second))
	_ = svc.SetPersonaStatus(ctx, p.ID, "retired")
	_ = svc.SetAccountStatus(ctx, acct.ID, "banned", nil)
}
