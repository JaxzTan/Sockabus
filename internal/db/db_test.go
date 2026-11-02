package db_test

// Integration tests against real PostgreSQL with migrations applied (TRD §9).
// Run: make test-db   (skipped when SOCKABUS_TEST_POSTGRES_URL is unset)

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JaxzTan/Buru_AI/internal/db"
	"github.com/JaxzTan/Buru_AI/internal/msgcrypt"
)

// setup returns queries inside a transaction that is rolled back after the test.
func setup(t *testing.T) (context.Context, *db.Queries, pgx.Tx) {
	url := os.Getenv("SOCKABUS_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("SOCKABUS_TEST_POSTGRES_URL not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx); _ = conn.Close(ctx) })
	return ctx, db.New(tx), tx
}

// must panics on error; the test fails with the error and its line.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// seedConversation creates operator → persona → account → conversation → one inbound message.
func seedConversation(t *testing.T, ctx context.Context, q *db.Queries) (db.Operator, db.Conversation, db.InsertMessageRow) {
	t.Helper()
	op := must(q.CreateOperator(ctx, db.CreateOperatorParams{Email: "op@sockabus.test", Role: db.OperatorRoleOperator, TotpSecretRef: "vault:op"}))
	p := must(q.CreatePersona(ctx, db.CreatePersonaParams{Name: "Auntie Mei", LanguageMix: json.RawMessage(`{"en":0.5,"hokkien":0.5}`)}))
	acc := must(q.CreateHoneypotAccount(ctx, db.CreateHoneypotAccountParams{PersonaID: p.ID, Channel: db.ChannelTelegram, Phone: "+60120000001", SessionRef: "vault:s1"}))
	c := must(q.CreateConversation(ctx, db.CreateConversationParams{AccountID: acc.ID, TemporalWorkflowID: "wf-1", ScammerHandle: pgtype.Text{String: "@scammer", Valid: true}}))
	m := must(q.InsertMessage(ctx, db.InsertMessageParams{ConversationID: c.ID, Direction: db.MessageDirectionInbound, SenderType: db.SenderTypeScammer, BodyCiphertext: []byte("x"), DekRef: "dek:1", SentAt: time.Now()}))
	return op, c, m
}

func TestPromptVersionsIncrement(t *testing.T) {
	ctx, q, _ := setup(t)
	p := must(q.CreatePersona(ctx, db.CreatePersonaParams{Name: "Uncle Lim", LanguageMix: json.RawMessage(`{"ms":1}`)}))
	v1 := must(q.CreatePromptVersion(ctx, db.CreatePromptVersionParams{PersonaID: p.ID, SystemPrompt: "a"}))
	v2 := must(q.CreatePromptVersion(ctx, db.CreatePromptVersionParams{PersonaID: p.ID, SystemPrompt: "b"}))
	if v1.Version != 1 || v2.Version != 2 {
		t.Fatalf("versions = %d, %d; want 1, 2", v1.Version, v2.Version)
	}
}

func TestEntityDedupeConfidenceAndVerify(t *testing.T) {
	ctx, q, _ := setup(t)
	op, c, m := seedConversation(t, ctx, q)
	if err := q.SetConversationScamType(ctx, db.SetConversationScamTypeParams{ID: c.ID, ScamType: db.NullScamType{ScamType: db.ScamTypeJob, Valid: true}}); err != nil {
		t.Fatal(err)
	}

	// Extraction asks for 'high'; FR-9 caps it at 'medium'. NULL bank_or_chain still dedupes (NULLS NOT DISTINCT).
	url := db.UpsertEntityParams{Type: db.EntityTypeUrl, ValueNormalized: "scam.top/verify", Confidence: db.ConfidenceLevelHigh}
	e1 := must(q.UpsertEntity(ctx, url))
	e2 := must(q.UpsertEntity(ctx, url))
	if e1.ID != e2.ID {
		t.Fatalf("dedupe failed: ids %d and %d", e1.ID, e2.ID)
	}
	if e1.Confidence != db.ConfidenceLevelMedium {
		t.Fatalf("confidence = %s; want medium", e1.Confidence)
	}

	// The same message counted twice is one sighting.
	s := db.InsertSightingParams{EntityID: e1.ID, ConversationID: c.ID, MessageID: m.ID}
	must(q.InsertSighting(ctx, s))
	if _, err := q.InsertSighting(ctx, s); err != pgx.ErrNoRows {
		t.Fatalf("duplicate sighting err = %v; want ErrNoRows", err)
	}
	if got := must(q.GetEntity(ctx, e1.ID)); got.SightingsCount != 1 {
		t.Fatalf("sightings_count = %d; want 1", got.SightingsCount)
	}

	v := must(q.VerifyEntity(ctx, db.VerifyEntityParams{EntityID: e1.ID, OperatorID: op.ID, Decision: db.VerificationDecisionVerified}))
	if v.Status != db.EntityStatusVerified || v.Confidence != db.ConfidenceLevelHigh {
		t.Fatalf("after verify: status %s, confidence %s; want verified, high", v.Status, v.Confidence)
	}
	if n := len(must(q.ListVerifications(ctx, e1.ID))); n != 1 {
		t.Fatalf("verifications = %d; want 1", n)
	}
	if st := must(q.EntityScamTypes(ctx, e1.ID)); len(st) != 1 || st[0] != db.ScamTypeJob {
		t.Fatalf("scam types = %v; want [job]", st)
	}
}

func TestLookupSingleAndBatch(t *testing.T) {
	ctx, q, _ := setup(t)
	bank := pgtype.Text{String: "MBB", Valid: true}
	acct := must(q.UpsertEntity(ctx, db.UpsertEntityParams{Type: db.EntityTypeBankAccount, ValueNormalized: "164204571893", BankOrChain: bank, Confidence: db.ConfidenceLevelMedium}))
	phone := must(q.UpsertEntity(ctx, db.UpsertEntityParams{Type: db.EntityTypePhone, ValueNormalized: "+60127774410", Confidence: db.ConfidenceLevelMedium}))
	batch := db.LookupEntitiesBatchParams{
		Types:  []string{"bank_account", "phone", "url"},
		Values: []string{"164204571893", "+60127774410", "not-there.com"},
		Banks:  []string{"MBB", "", ""},
	}

	// Lookup policy A: an unverified bank account (or wallet) never matches; an unverified phone does.
	if _, err := q.LookupEntity(ctx, db.LookupEntityParams{Type: db.EntityTypeBankAccount, ValueNormalized: "164204571893", BankOrChain: bank}); err != pgx.ErrNoRows {
		t.Fatalf("unverified bank account lookup err = %v; want ErrNoRows", err)
	}
	if got := must(q.LookupEntity(ctx, db.LookupEntityParams{Type: db.EntityTypePhone, ValueNormalized: "+60127774410"})); got.ID != phone.ID {
		t.Fatalf("unverified phone lookup id = %d; want %d", got.ID, phone.ID)
	}
	if rows := must(q.LookupEntitiesBatch(ctx, batch)); len(rows) != 1 || rows[0].ID != phone.ID {
		t.Fatalf("batch before verification = %d rows; want only the phone", len(rows))
	}

	op := must(q.CreateOperator(ctx, db.CreateOperatorParams{Email: "lookup@sockabus.test", Role: db.OperatorRoleOperator, TotpSecretRef: "vault:lookup"}))
	must(q.VerifyEntity(ctx, db.VerifyEntityParams{EntityID: acct.ID, OperatorID: op.ID, Decision: db.VerificationDecisionVerified}))

	got := must(q.LookupEntity(ctx, db.LookupEntityParams{Type: db.EntityTypeBankAccount, ValueNormalized: "164204571893", BankOrChain: bank}))
	if got.ID != acct.ID || got.Status != db.EntityStatusVerified {
		t.Fatalf("verified lookup = id %d status %s; want %d verified", got.ID, got.Status, acct.ID)
	}
	if _, err := q.LookupEntity(ctx, db.LookupEntityParams{Type: db.EntityTypeBankAccount, ValueNormalized: "164204571893"}); err != pgx.ErrNoRows {
		t.Fatalf("lookup without bank err = %v; want ErrNoRows", err)
	}
	if rows := must(q.LookupEntitiesBatch(ctx, batch)); len(rows) != 2 {
		t.Fatalf("batch after verification = %d; want 2", len(rows))
	}
	// A rejected phone stops matching.
	must(q.VerifyEntity(ctx, db.VerifyEntityParams{EntityID: phone.ID, OperatorID: op.ID, Decision: db.VerificationDecisionRejected}))
	if _, err := q.LookupEntity(ctx, db.LookupEntityParams{Type: db.EntityTypePhone, ValueNormalized: "+60127774410"}); err != pgx.ErrNoRows {
		t.Fatalf("rejected phone lookup err = %v; want ErrNoRows", err)
	}
}

// 000014: RLS is on for every table, and the policies let the app roles through (anon/authenticated
// get nothing: no grants (000003) and no policy). SET ROLE needs a superuser test login; where it is
// not allowed (Supabase) the policy rows are checked instead.
func TestRowLevelSecurity(t *testing.T) {
	ctx, _, tx := setup(t)
	rows := must(tx.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relrowsecurity ORDER BY 1`))
	off := must(pgx.CollectRows(rows, pgx.RowTo[string]))
	if len(off) > 0 {
		t.Fatalf("tables without row level security: %v", off)
	}
	var missing []string
	rows = must(tx.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT EXISTS (
			SELECT 1 FROM pg_policies p WHERE p.schemaname = 'public' AND p.tablename = c.relname AND 'buru_app' = ANY (p.roles))
		ORDER BY 1`))
	missing = must(pgx.CollectRows(rows, pgx.RowTo[string]))
	if len(missing) > 0 {
		t.Fatalf("tables with no buru_app policy (the app would see no rows): %v", missing)
	}
	var super bool
	if err := tx.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil {
		t.Fatal(err)
	}
	if !super {
		return
	}
	// As buru_app (not the owner, so RLS applies): reads see rows, writes pass the policy check.
	if _, err := tx.Exec(ctx, `INSERT INTO entities (type, value_normalized, confidence, first_seen, last_seen) VALUES ('url', 'rls-owner.test', 'low', now(), now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE buru_app`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM entities WHERE value_normalized = 'rls-owner.test'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("buru_app sees %d owner rows (%v); want 1", n, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entities (type, value_normalized, confidence, first_seen, last_seen) VALUES ('url', 'rls-app.test', 'low', now(), now())`); err != nil {
		t.Fatalf("buru_app insert blocked by RLS: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE entities SET sightings_count = sightings_count WHERE value_normalized = 'rls-app.test'`); err != nil {
		t.Fatalf("buru_app update blocked by RLS: %v", err)
	}
}

// NFR-8: the app role can add audit records but never change or remove them.
// Checks grants directly, so it also runs where the test user cannot SET ROLE (Supabase's postgres is not superuser).
func TestAppendOnlyForAppRole(t *testing.T) {
	ctx, _, tx := setup(t)
	for _, table := range []string{"messages", "entity_sightings", "verifications",
		"conversation_summaries", "persona_summaries", "overview_summaries", "conversation_stage_events", "account_health_events"} {
		for priv, want := range map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": false, "DELETE": false, "TRUNCATE": false} {
			var got bool
			if err := tx.QueryRow(ctx, "SELECT has_table_privilege('buru_app', $1, $2)", table, priv).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("buru_app %s on %s = %v; want %v", priv, table, got, want)
			}
		}
	}
}

// bank_or_chain is written upper case now; rows from before may be lower case ('telegram'). The
// upsert must reuse the old row (no duplicate entity, no rewrite) and lookups must still match it.
func TestBankOrChainCaseInsensitive(t *testing.T) {
	ctx, q, tx := setup(t)
	// An old lower-case row, inserted the way the previous code did.
	var oldID int64
	if err := tx.QueryRow(ctx, `INSERT INTO entities (type, value_normalized, bank_or_chain, confidence, first_seen, last_seen)
		VALUES ('handle', '@case_test_scammer', 'telegram', 'low', now(), now()) RETURNING id`).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	up := db.UpsertEntityParams{Type: db.EntityTypeHandle, ValueNormalized: "@case_test_scammer", BankOrChain: pgtype.Text{String: "TELEGRAM", Valid: true}, Confidence: db.ConfidenceLevelLow}
	got := must(q.UpsertEntity(ctx, up))
	if got.ID != oldID {
		t.Fatalf("upsert created entity %d instead of reusing %d", got.ID, oldID)
	}
	if got.BankOrChain.String != "telegram" {
		t.Fatalf("existing bank_or_chain rewritten to %q", got.BankOrChain.String)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM entities WHERE value_normalized = '@case_test_scammer'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("entities for the handle = %d (%v); want 1", n, err)
	}
	for _, b := range []string{"TELEGRAM", "telegram", "Telegram"} {
		e, err := q.LookupEntity(ctx, db.LookupEntityParams{Type: db.EntityTypeHandle, ValueNormalized: "@case_test_scammer", BankOrChain: pgtype.Text{String: b, Valid: true}})
		if err != nil || e.ID != oldID {
			t.Fatalf("lookup with %q: id %d err %v", b, e.ID, err)
		}
	}
	// A brand-new value is inserted upper case, and the second upsert dedupes onto it.
	nw := db.UpsertEntityParams{Type: db.EntityTypeWallet, ValueNormalized: "TCaseTestWallet111111111111111111", BankOrChain: pgtype.Text{String: "TRON", Valid: true}, Confidence: db.ConfidenceLevelMedium}
	a, b := must(q.UpsertEntity(ctx, nw)), must(q.UpsertEntity(ctx, nw))
	if a.ID != b.ID || a.BankOrChain.String != "TRON" {
		t.Fatalf("new entity: %d/%d %q", a.ID, b.ID, a.BankOrChain.String)
	}
	rows := must(q.LookupEntitiesBatch(ctx, db.LookupEntitiesBatchParams{Types: []string{"handle"}, Values: []string{"@case_test_scammer"}, Banks: []string{"TELEGRAM"}}))
	if len(rows) != 1 {
		t.Fatalf("batch lookup matches = %d; want 1", len(rows))
	}
}

// testBox is a fixed-key msgcrypt box for the blind index / ciphertext columns (000012).
func testBox(t *testing.T) *msgcrypt.Box {
	t.Helper()
	b, err := msgcrypt.New(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// peerParams builds CreateConversationForPeer params the way honeypot.Service.openConversation does:
// ciphertext + blind index always, plaintext only in phase 1 (plaintext=true).
func peerParams(t *testing.T, box *msgcrypt.Box, accountID int64, wf, handle, peer string, plaintext bool) db.CreateConversationForPeerParams {
	t.Helper()
	hct, ref := must2(box.Seal([]byte(handle), msgcrypt.AADConvHandle(accountID)))
	pct, _ := must2(box.Seal([]byte(peer), msgcrypt.AADConvPeerRef(accountID)))
	return db.CreateConversationForPeerParams{AccountID: accountID, TemporalWorkflowID: wf, StartedAt: time.Now(),
		ScammerHandle: pgtype.Text{String: handle, Valid: plaintext}, ScammerPeerRef: pgtype.Text{String: peer, Valid: plaintext},
		ScammerHandleCiphertext: hct, ScammerPeerRefCiphertext: pct, ScammerPeerRefBidx: box.PeerRefIndex(peer),
		IdentifiersDekRef: pgtype.Text{String: ref, Valid: true}}
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

// Concurrent first messages from one scammer must converge on one open conversation
// (conversations_open_peer_bidx_uq + CreateConversationForPeer ON CONFLICT DO NOTHING + re-select,
// as honeypot.Service.openConversation does), both while the plaintext is still written (phase 1)
// and after (phase 2, blind index only). Needs committed rows, so it uses a pool and deletes what it created.
func TestConcurrentFirstMessagesOneConversation(t *testing.T) {
	url := os.Getenv("SOCKABUS_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("SOCKABUS_TEST_POSTGRES_URL not set")
	}
	box := testBox(t)
	for _, plaintext := range []bool{true, false} {
		t.Run(fmt.Sprintf("plaintext=%v", plaintext), func(t *testing.T) {
			ctx := context.Background()
			pool, err := pgxpool.New(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			q := db.New(pool)
			stamp := time.Now().UnixNano()
			p := must(q.CreatePersona(ctx, db.CreatePersonaParams{Name: "Race Test", LanguageMix: json.RawMessage(`{"en":1}`)}))
			acc := must(q.CreateHoneypotAccount(ctx, db.CreateHoneypotAccountParams{PersonaID: p.ID, Channel: db.ChannelTelegram, Phone: fmt.Sprintf("race-%d", stamp), SessionRef: "test:race"}))
			t.Cleanup(func() {
				_, _ = pool.Exec(ctx, "DELETE FROM conversations WHERE account_id = $1", acc.ID)
				_, _ = pool.Exec(ctx, "DELETE FROM honeypot_accounts WHERE id = $1", acc.ID)
				_, _ = pool.Exec(ctx, "DELETE FROM personas WHERE id = $1", p.ID)
				pool.Close()
			})
			peer := fmt.Sprintf("tg:user:%d:1", stamp)
			find := db.FindOpenConversationByPeerParams{AccountID: acc.ID, PeerBidx: box.PeerRefIndex(peer), PeerRef: peer}

			const n = 12
			ids := make([]int64, n)
			created := make([]bool, n)
			errs := make([]error, n)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					c, err := q.CreateConversationForPeer(ctx, peerParams(t, box, acc.ID, fmt.Sprintf("race-%d-%d", stamp, i), "@race", peer, plaintext))
					if errors.Is(err, pgx.ErrNoRows) {
						c, err = q.FindOpenConversationByPeer(ctx, find)
					} else if err == nil {
						created[i] = true
					}
					ids[i], errs[i] = c.ID, err
				}(i)
			}
			close(start)
			wg.Wait()
			winners := 0
			for i := 0; i < n; i++ {
				if errs[i] != nil {
					t.Fatalf("goroutine %d: %v", i, errs[i])
				}
				if ids[i] != ids[0] {
					t.Fatalf("goroutine %d got conversation %d, goroutine 0 got %d", i, ids[i], ids[0])
				}
				if created[i] {
					winners++
				}
			}
			var open int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM conversations WHERE account_id = $1 AND status <> 'closed'", acc.ID).Scan(&open); err != nil {
				t.Fatal(err)
			}
			if open != 1 || winners != 1 {
				t.Fatalf("open conversations = %d, inserts that won = %d; want 1 and 1", open, winners)
			}
			// After it is closed, the same peer can open a new conversation.
			must(q.SetConversationStatus(ctx, db.SetConversationStatusParams{ID: ids[0], Status: db.ConversationStatusClosed}))
			again := must(q.CreateConversationForPeer(ctx, peerParams(t, box, acc.ID, fmt.Sprintf("race-%d-again", stamp), "@race", peer, plaintext)))
			if again.ID == ids[0] {
				t.Fatal("closed conversation reused")
			}
		})
	}
}

// A conversation written before 000012 (plaintext only, no blind index) is still found by routing,
// still blocks a second open conversation for the same peer, and after the backfill query fills its
// ciphertext it is found by the blind index alone.
func TestPeerRoutingFallbackAndBackfill(t *testing.T) {
	ctx, q, tx := setup(t)
	box := testBox(t)
	p := must(q.CreatePersona(ctx, db.CreatePersonaParams{Name: "Legacy", LanguageMix: json.RawMessage(`{"en":1}`)}))
	acc := must(q.CreateHoneypotAccount(ctx, db.CreateHoneypotAccountParams{PersonaID: p.ID, Channel: db.ChannelTelegram, Phone: "+60120000777", SessionRef: "vault:legacy"}))
	peer := "tg:user:4242:99999"
	var legacyID int64
	// The pre-000012 insert (old app build): plaintext columns only.
	if err := tx.QueryRow(ctx, `INSERT INTO conversations (account_id, temporal_workflow_id, scammer_handle, scammer_peer_ref, started_at)
		VALUES ($1, 'wf-legacy', '@legacy', $2, now()) RETURNING id`, acc.ID, peer).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	find := db.FindOpenConversationByPeerParams{AccountID: acc.ID, PeerBidx: box.PeerRefIndex(peer), PeerRef: peer}
	if c := must(q.FindOpenConversationByPeer(ctx, find)); c.ID != legacyID {
		t.Fatalf("fallback routing found %d, want %d", c.ID, legacyID)
	}
	// New app, phase 1 (plaintext also written): conversations_open_peer_uq still blocks the duplicate.
	if _, err := q.CreateConversationForPeer(ctx, peerParams(t, box, acc.ID, "wf-dup", "@legacy", peer, true)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("duplicate open conversation for a legacy row: err = %v, want no row", err)
	}
	// Backfill (cmd/backfill-encrypt): fill the ciphertext and blind index.
	todo := must(q.ListConversationsMissingCiphertext(ctx))
	if len(todo) == 0 {
		t.Fatal("legacy row not listed for backfill")
	}
	bp := peerParams(t, box, acc.ID, "", "@legacy", peer, false)
	n := must(q.SetConversationIdentifiersCiphertext(ctx, db.SetConversationIdentifiersCiphertextParams{ID: legacyID,
		HandleCiphertext: bp.ScammerHandleCiphertext, PeerRefCiphertext: bp.ScammerPeerRefCiphertext, PeerRefBidx: bp.ScammerPeerRefBidx, DekRef: bp.IdentifiersDekRef}))
	if n != 1 {
		t.Fatalf("backfill updated %d rows, want 1", n)
	}
	if n := must(q.SetConversationIdentifiersCiphertext(ctx, db.SetConversationIdentifiersCiphertextParams{ID: legacyID,
		HandleCiphertext: bp.ScammerHandleCiphertext, PeerRefCiphertext: bp.ScammerPeerRefCiphertext, PeerRefBidx: bp.ScammerPeerRefBidx, DekRef: bp.IdentifiersDekRef})); n != 0 {
		t.Fatalf("second backfill updated %d rows, want 0 (idempotent)", n)
	}
	// Phase 2 simulated: plaintext gone; routing must use the blind index only.
	if _, err := tx.Exec(ctx, `UPDATE conversations SET scammer_handle = NULL, scammer_peer_ref = NULL WHERE id = $1`, legacyID); err != nil {
		t.Fatal(err)
	}
	if c := must(q.FindOpenConversationByPeer(ctx, find)); c.ID != legacyID {
		t.Fatalf("blind-index routing found %d, want %d", c.ID, legacyID)
	}
	// And conversations_open_peer_bidx_uq blocks a duplicate written without plaintext.
	if _, err := q.CreateConversationForPeer(ctx, peerParams(t, box, acc.ID, "wf-dup2", "@legacy", peer, false)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("duplicate open conversation by blind index: err = %v, want no row", err)
	}
	// A different peer is not matched.
	other := db.FindOpenConversationByPeerParams{AccountID: acc.ID, PeerBidx: box.PeerRefIndex(peer + "0"), PeerRef: peer + "0"}
	if _, err := q.FindOpenConversationByPeer(ctx, other); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("other peer: err = %v, want no rows", err)
	}
}

// Summaries are written sealed (000011): summary NULL, ciphertext + dek_ref set; a row with neither is refused.
func TestSummaryCiphertextConstraints(t *testing.T) {
	ctx, q, tx := setup(t)
	_, c, _ := seedConversation(t, ctx, q)
	row := must(q.InsertConversationSummary(ctx, db.InsertConversationSummaryParams{ConversationID: c.ID, SummaryCiphertext: []byte("sealed"),
		DekRef: pgtype.Text{String: "local:v1", Valid: true}, Stage: "rapport", Model: "test", MessageCountAt: 1, Reason: "on_demand"}))
	if row.Summary.Valid {
		t.Fatal("plaintext summary stored")
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT s"); err != nil {
		t.Fatal(err)
	}
	_, err := q.InsertConversationSummary(ctx, db.InsertConversationSummaryParams{ConversationID: c.ID, Stage: "rapport", Model: "test", MessageCountAt: 1, Reason: "on_demand"})
	if err == nil {
		t.Fatal("summary row with neither plaintext nor ciphertext accepted")
	}
}

// Retention purge (000013): dry run counts and keeps; a real run deletes old messages, their sightings
// and old summaries, keeps recent rows, entities and verifications, and writes an audit row each time.
// buru_app cannot execute it, the 12-month floor holds, and the append-only triggers still block
// everything outside the function.
func TestRetentionPurge(t *testing.T) {
	ctx, q, tx := setup(t)
	op, c, recentMsg := seedConversation(t, ctx, q)
	old := time.Now().AddDate(-1, -1, 0) // 13 months ago
	oldMsg := must(q.InsertMessage(ctx, db.InsertMessageParams{ConversationID: c.ID, Direction: db.MessageDirectionInbound, SenderType: db.SenderTypeScammer,
		BodyCiphertext: []byte("old"), DekRef: "local:v1", SentAt: old}))
	e := must(q.UpsertEntity(ctx, db.UpsertEntityParams{Type: db.EntityTypeBankAccount, ValueNormalized: fmt.Sprintf("1642%d", time.Now().UnixNano()%1e8), Confidence: db.ConfidenceLevelLow}))
	must(q.InsertSighting(ctx, db.InsertSightingParams{EntityID: e.ID, ConversationID: c.ID, MessageID: oldMsg.ID}))
	must(q.InsertSighting(ctx, db.InsertSightingParams{EntityID: e.ID, ConversationID: c.ID, MessageID: recentMsg.ID}))
	must(q.VerifyEntity(ctx, db.VerifyEntityParams{EntityID: e.ID, OperatorID: op.ID, Decision: db.VerificationDecisionVerified}))
	if _, err := tx.Exec(ctx, `INSERT INTO conversation_summaries (conversation_id, summary, stage, model, message_count_at, created_at)
		VALUES ($1, 'old summary', 'rapport', 'test', 1, $2)`, c.ID, old); err != nil {
		t.Fatal(err)
	}
	year := pgtype.Interval{Months: 12, Valid: true}
	count := func(sql string, args ...any) int64 {
		var n int64
		if err := tx.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	dry := must(q.PurgeExpired(ctx, db.PurgeExpiredParams{Retention: year, DryRun: true, RequestedBy: "db_test"}))
	if !dry.DryRun || dry.MessagesDeleted < 1 || dry.SightingsDeleted < 1 || dry.SummariesDeleted < 1 || dry.RequestedBy != "db_test" {
		t.Fatalf("dry run = %+v; want >=1 message, sighting, summary", dry)
	}
	if count("SELECT count(*) FROM messages WHERE id = $1", oldMsg.ID) != 1 {
		t.Fatal("dry run deleted the old message")
	}

	real := must(q.PurgeExpired(ctx, db.PurgeExpiredParams{Retention: year, DryRun: false, RequestedBy: "db_test"}))
	if real.DryRun || real.MessagesDeleted != dry.MessagesDeleted || real.SightingsDeleted != dry.SightingsDeleted || real.SummariesDeleted != dry.SummariesDeleted {
		t.Fatalf("real run = %+v, dry run = %+v; counts must match", real, dry)
	}
	if count("SELECT count(*) FROM messages WHERE id = $1", oldMsg.ID) != 0 || count("SELECT count(*) FROM entity_sightings WHERE message_id = $1", oldMsg.ID) != 0 {
		t.Fatal("old message or its sighting survived the purge")
	}
	if count("SELECT count(*) FROM messages WHERE id = $1", recentMsg.ID) != 1 || count("SELECT count(*) FROM entity_sightings WHERE message_id = $1", recentMsg.ID) != 1 {
		t.Fatal("recent message or its sighting was purged")
	}
	if count("SELECT count(*) FROM entities WHERE id = $1", e.ID) != 1 || count("SELECT count(*) FROM verifications WHERE entity_id = $1", e.ID) != 1 {
		t.Fatal("entity or verification was purged")
	}
	if count("SELECT count(*) FROM retention_purges WHERE id IN ($1, $2)", dry.ID, real.ID) != 2 {
		t.Fatal("audit rows missing")
	}

	// Each refusal runs in a savepoint so the test transaction stays usable.
	refused := func(name, sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, "SAVEPOINT r"); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(ctx, sql, args...)
		if _, rerr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT r"); rerr != nil {
			t.Fatal(rerr)
		}
		if err == nil {
			t.Fatalf("%s: allowed, want refused", name)
		}
	}
	refused("buru_app executes the purge", "SET LOCAL ROLE buru_app; SELECT buru_purge_expired(interval '12 months', true, 'app')")
	refused("retention below 12 months", "SELECT buru_purge_expired(interval '30 days', true, 'typo')")
	refused("DELETE outside the purge", "DELETE FROM messages WHERE id = $1", recentMsg.ID)
	refused("UPDATE with the purge flag set", "SELECT set_config('buru.allow_purge', 'on', true); UPDATE messages SET dek_ref = 'x' WHERE id = "+fmt.Sprint(recentMsg.ID))
	refused("DELETE verifications with the purge flag set", "SELECT set_config('buru.allow_purge', 'on', true); DELETE FROM verifications WHERE entity_id = "+fmt.Sprint(e.ID))
	refused("DELETE an audit row", "DELETE FROM retention_purges WHERE id = $1", real.ID)
}
