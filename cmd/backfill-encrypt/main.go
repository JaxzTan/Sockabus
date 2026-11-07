// Command backfill-encrypt fills the encrypted scammer-identifier columns of migration 000012 for rows
// written before the app encrypted them (TRD §7): conversations.scammer_handle / scammer_peer_ref
// (ciphertext + keyed blind index) and personas.demo_identifiers (ciphertext). It never changes or
// clears the plaintext columns (phase 2, db/migrations-pending/*_drop_plaintext_identifiers, does that).
//
//	SOCKABUS_MESSAGE_KEY=... go run ./cmd/backfill-encrypt -db 'postgres://...'                 # dry run (default)
//	SOCKABUS_MESSAGE_KEY=... go run ./cmd/backfill-encrypt -db 'postgres://...' -dry-run=false   # write
//	SOCKABUS_MESSAGE_KEY=... go run ./cmd/backfill-encrypt -db 'postgres://...' -verify          # read-only check
//	SOCKABUS_MESSAGE_KEY=... go run ./cmd/backfill-encrypt -db 'postgres://...' -restore -dry-run=false
//
// -restore undoes phase 2: it decrypts the ciphertext back into plaintext columns that are NULL.
// Idempotent: a rerun only touches rows still missing a value, so it is safe to run again after rows
// were written in between. The key comes from the environment only (never a flag: shell history).
// It must be the app's SOCKABUS_MESSAGE_KEY, or the app cannot read what this writes.
// It refuses a Supabase (production) URL unless -allow-production is given.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JaxzTan/Buru_AI/internal/db"
	"github.com/JaxzTan/Buru_AI/internal/msgcrypt"
)

type opts struct {
	url                     string
	dryRun, verify, restore bool
	prod                    bool
}

func main() {
	var o opts
	flag.StringVar(&o.url, "db", "", "PostgreSQL URL (required; never read from .env)")
	flag.BoolVar(&o.dryRun, "dry-run", true, "report what would change without writing (default true; -dry-run=false writes)")
	flag.BoolVar(&o.verify, "verify", false, "read-only: decrypt every ciphertext, compare with the plaintext and the blind index")
	flag.BoolVar(&o.restore, "restore", false, "phase-2 rollback: decrypt ciphertext back into NULL plaintext columns")
	flag.BoolVar(&o.prod, "allow-production", false, "allow a Supabase URL")
	flag.Parse()
	problems, err := run(o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "backfill-encrypt:", err)
		os.Exit(1)
	}
	if problems > 0 {
		os.Exit(3)
	}
}

func run(o opts) (int, error) {
	if o.url == "" {
		return 0, errors.New("-db is required")
	}
	if strings.Contains(o.url, "supabase") && !o.prod {
		return 0, errors.New("refusing a Supabase (production) URL without -allow-production")
	}
	if o.verify && o.restore {
		return 0, errors.New("-verify and -restore are exclusive")
	}
	box, err := msgcrypt.New(os.Getenv("SOCKABUS_MESSAGE_KEY"))
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, o.url)
	if err != nil {
		return 0, err
	}
	defer conn.Close(ctx)

	if err := checkKey(ctx, db.New(conn), box); err != nil {
		return 0, err
	}
	if o.verify {
		return verify(ctx, db.New(conn), box)
	}
	// One transaction: a real run either fills every row or none; a dry run rolls back.
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)
	mode := "DRY RUN (nothing written; -dry-run=false to write)"
	if !o.dryRun {
		mode = "WRITE"
	}
	fmt.Println("backfill-encrypt:", mode)
	if o.restore {
		err = restore(ctx, q, box)
	} else {
		err = backfill(ctx, q, box)
	}
	if err != nil {
		return 0, err
	}
	if o.dryRun {
		return 0, nil
	}
	return 0, tx.Commit(ctx)
}

// checkKey refuses to run unless the key opens message bodies the app sealed: a backfill under any other
// key would write blind indexes the app never computes, and routing would stop finding those conversations
// (-verify alone cannot tell, it checks against the same wrong key).
func checkKey(ctx context.Context, q *db.Queries, box *msgcrypt.Box) error {
	msgs, err := q.RecentMessagesForKeyCheck(ctx)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		fmt.Println("key check: no messages yet, cannot prove this is the app's key (fresh database)")
		return nil
	}
	for _, m := range msgs {
		if _, err := box.Open(m.BodyCiphertext, m.DekRef, []byte(fmt.Sprintf("conversation:%d", m.ConversationID))); err == nil {
			fmt.Printf("key check: OK (opens message %d sealed by the app)\n", m.ID)
			return nil
		}
	}
	return fmt.Errorf("SOCKABUS_MESSAGE_KEY does not open any of the %d newest messages: it is not the key the app uses; nothing written", len(msgs))
}

func backfill(ctx context.Context, q *db.Queries, box *msgcrypt.Box) error {
	convs, err := q.ListConversationsMissingCiphertext(ctx)
	if err != nil {
		return err
	}
	var filled int64
	for _, c := range convs {
		p := db.SetConversationIdentifiersCiphertextParams{ID: c.ID}
		ref := ""
		if c.ScammerHandle.Valid {
			if p.HandleCiphertext, ref, err = box.Seal([]byte(c.ScammerHandle.String), msgcrypt.AADConvHandle(c.AccountID)); err != nil {
				return err
			}
		}
		if c.ScammerPeerRef.Valid {
			if p.PeerRefCiphertext, ref, err = box.Seal([]byte(c.ScammerPeerRef.String), msgcrypt.AADConvPeerRef(c.AccountID)); err != nil {
				return err
			}
			p.PeerRefBidx = box.PeerRefIndex(c.ScammerPeerRef.String)
		}
		p.DekRef = pgtype.Text{String: ref, Valid: ref != ""}
		n, err := q.SetConversationIdentifiersCiphertext(ctx, p)
		if err != nil {
			return fmt.Errorf("conversation %d: %w", c.ID, err)
		}
		filled += n
	}
	fmt.Printf("conversations: %d missing ciphertext, %d filled\n", len(convs), filled)

	ps, err := q.ListPersonasMissingCiphertext(ctx)
	if err != nil {
		return err
	}
	filled = 0
	for _, p := range ps {
		ct, ref, err := box.Seal(p.DemoIdentifiers, msgcrypt.AADPersonaDemoIDs(p.ID))
		if err != nil {
			return err
		}
		n, err := q.SetPersonaDemoIdentifiersCiphertext(ctx, db.SetPersonaDemoIdentifiersCiphertextParams{ID: p.ID,
			DemoIdentifiersCiphertext: ct, DemoIdentifiersDekRef: pgtype.Text{String: ref, Valid: true}})
		if err != nil {
			return fmt.Errorf("persona %d: %w", p.ID, err)
		}
		filled += n
	}
	fmt.Printf("personas: %d missing demo_identifiers ciphertext, %d filled\n", len(ps), filled)

	n, err := q.CountPlaintextSummaries(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("summaries: %d pre-000011 plaintext rows (append-only: left as they are, read as fallback, removed by retention)\n", n)
	return nil
}

func restore(ctx context.Context, q *db.Queries, box *msgcrypt.Box) error {
	convs, err := q.ListConversationIdentifiers(ctx)
	if err != nil {
		return err
	}
	var restored int64
	for _, c := range convs {
		if c.ScammerHandle.Valid || c.ScammerPeerRef.Valid || (len(c.ScammerHandleCiphertext) == 0 && len(c.ScammerPeerRefCiphertext) == 0) {
			continue
		}
		var handle, peer pgtype.Text
		if len(c.ScammerHandleCiphertext) > 0 {
			pt, err := box.Open(c.ScammerHandleCiphertext, c.IdentifiersDekRef.String, msgcrypt.AADConvHandle(c.AccountID))
			if err != nil {
				return fmt.Errorf("conversation %d handle: %w", c.ID, err)
			}
			handle = pgtype.Text{String: string(pt), Valid: true}
		}
		if len(c.ScammerPeerRefCiphertext) > 0 {
			pt, err := box.Open(c.ScammerPeerRefCiphertext, c.IdentifiersDekRef.String, msgcrypt.AADConvPeerRef(c.AccountID))
			if err != nil {
				return fmt.Errorf("conversation %d peer ref: %w", c.ID, err)
			}
			peer = pgtype.Text{String: string(pt), Valid: true}
		}
		n, err := q.RestoreConversationPlaintextIdentifiers(ctx, db.RestoreConversationPlaintextIdentifiersParams{ID: c.ID, ScammerHandle: handle, ScammerPeerRef: peer})
		if err != nil {
			return fmt.Errorf("conversation %d: %w", c.ID, err)
		}
		restored += n
	}
	fmt.Printf("conversations: %d plaintext identifiers restored\n", restored)

	ps, err := q.ListPersonaDemoIdentifiers(ctx)
	if err != nil {
		return err
	}
	restored = 0
	for _, p := range ps {
		if p.DemoIdentifiers != nil || len(p.DemoIdentifiersCiphertext) == 0 {
			continue
		}
		pt, err := box.Open(p.DemoIdentifiersCiphertext, p.DemoIdentifiersDekRef.String, msgcrypt.AADPersonaDemoIDs(p.ID))
		if err != nil {
			return fmt.Errorf("persona %d: %w", p.ID, err)
		}
		n, err := q.RestorePersonaPlaintextDemoIdentifiers(ctx, db.RestorePersonaPlaintextDemoIdentifiersParams{ID: p.ID, DemoIdentifiers: json.RawMessage(pt)})
		if err != nil {
			return fmt.Errorf("persona %d: %w", p.ID, err)
		}
		restored += n
	}
	fmt.Printf("personas: %d plaintext demo_identifiers restored\n", restored)
	fmt.Println("note: the pending drop_plaintext_identifiers CHECKs must be dropped (its down migration) before -restore can write")
	return nil
}

// verify checks every row: ciphertext present where plaintext is, ciphertext opens with this key and
// equals the plaintext, and the blind index matches. It returns the number of problems found.
func verify(ctx context.Context, q *db.Queries, box *msgcrypt.Box) (int, error) {
	problems := 0
	bad := func(format string, a ...any) {
		problems++
		fmt.Printf("PROBLEM: "+format+"\n", a...)
	}
	convs, err := q.ListConversationIdentifiers(ctx)
	if err != nil {
		return 0, err
	}
	sealed := 0
	for _, c := range convs {
		if c.ScammerHandle.Valid && len(c.ScammerHandleCiphertext) == 0 {
			bad("conversation %d: handle has no ciphertext (run the backfill)", c.ID)
		}
		if c.ScammerPeerRef.Valid && len(c.ScammerPeerRefCiphertext) == 0 {
			bad("conversation %d: peer ref has no ciphertext / blind index (run the backfill)", c.ID)
		}
		if len(c.ScammerHandleCiphertext) > 0 {
			pt, err := box.Open(c.ScammerHandleCiphertext, c.IdentifiersDekRef.String, msgcrypt.AADConvHandle(c.AccountID))
			switch {
			case err != nil:
				bad("conversation %d: handle ciphertext does not open with this key: %v", c.ID, err)
			case c.ScammerHandle.Valid && string(pt) != c.ScammerHandle.String:
				bad("conversation %d: handle ciphertext differs from the plaintext", c.ID)
			}
		}
		if len(c.ScammerPeerRefCiphertext) > 0 {
			pt, err := box.Open(c.ScammerPeerRefCiphertext, c.IdentifiersDekRef.String, msgcrypt.AADConvPeerRef(c.AccountID))
			switch {
			case err != nil:
				bad("conversation %d: peer ref ciphertext does not open with this key: %v", c.ID, err)
			case c.ScammerPeerRef.Valid && string(pt) != c.ScammerPeerRef.String:
				bad("conversation %d: peer ref ciphertext differs from the plaintext", c.ID)
			case !bytes.Equal(box.PeerRefIndex(string(pt)), c.ScammerPeerRefBidx):
				bad("conversation %d: blind index does not match the peer ref (routing would miss it)", c.ID)
			default:
				sealed++
			}
		}
	}
	fmt.Printf("conversations: %d rows, %d with a verified peer ref ciphertext + blind index\n", len(convs), sealed)

	ps, err := q.ListPersonaDemoIdentifiers(ctx)
	if err != nil {
		return 0, err
	}
	sealed = 0
	for _, p := range ps {
		if p.DemoIdentifiers != nil && len(p.DemoIdentifiersCiphertext) == 0 {
			bad("persona %d: demo_identifiers has no ciphertext (run the backfill)", p.ID)
			continue
		}
		if len(p.DemoIdentifiersCiphertext) == 0 {
			continue
		}
		pt, err := box.Open(p.DemoIdentifiersCiphertext, p.DemoIdentifiersDekRef.String, msgcrypt.AADPersonaDemoIDs(p.ID))
		if err != nil {
			bad("persona %d: demo_identifiers ciphertext does not open with this key: %v", p.ID, err)
			continue
		}
		if p.DemoIdentifiers != nil && !jsonEqual(pt, p.DemoIdentifiers) {
			bad("persona %d: demo_identifiers ciphertext differs from the plaintext", p.ID)
			continue
		}
		sealed++
	}
	fmt.Printf("personas: %d rows, %d with verified demo_identifiers ciphertext\n", len(ps), sealed)
	if problems == 0 {
		fmt.Println("verify: OK (0 problems)")
	} else {
		fmt.Printf("verify: %d problems\n", problems)
	}
	return problems, nil
}

// jsonEqual compares JSON by value: jsonb normalises key order and spacing.
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return bytes.Equal(xa, ya)
}
