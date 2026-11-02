// Command retention-purge runs the TRD §7 retention purge (migration 000013, buru_purge_expired):
// messages older than the retention (minimum and default 12 months), their entity_sightings and old AI
// summaries are deleted; entities and verifications are kept; every run (dry or real) writes an audit
// row to retention_purges. It is not scheduled anywhere: run it deliberately.
//
//	make retention-purge DB='postgres://...'              # dry run (default)
//	make retention-purge DB='postgres://...' DRY_RUN=0    # delete
//	go run ./cmd/retention-purge -db '...' -dry-run=false -by 'jaxz'
//
// The database user must own the function or be a member of buru_purge; buru_app cannot run it.
// It refuses a Supabase (production) URL unless -allow-production is given.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JaxzTan/Buru_AI/internal/db"
)

func main() {
	url := flag.String("db", "", "PostgreSQL URL (required; never read from .env)")
	dryRun := flag.Bool("dry-run", true, "count what would be deleted, delete nothing (default true; -dry-run=false deletes)")
	months := flag.Int("months", 12, "retention in months (minimum 12, TRD §7)")
	by := flag.String("by", "", "operator or job name for the audit row (default: OS user)")
	prod := flag.Bool("allow-production", false, "allow a Supabase URL")
	flag.Parse()
	if err := run(*url, *dryRun, *months, *by, *prod); err != nil {
		fmt.Fprintln(os.Stderr, "retention-purge:", err)
		os.Exit(1)
	}
}

func run(url string, dryRun bool, months int, by string, prod bool) error {
	if url == "" {
		return errors.New("-db is required")
	}
	if strings.Contains(url, "supabase") && !prod {
		return errors.New("refusing a Supabase (production) URL without -allow-production")
	}
	if months < 12 {
		return fmt.Errorf("-months %d is below the 12-month minimum (TRD §7)", months)
	}
	if by == "" {
		if u, err := user.Current(); err == nil {
			by = "cli:" + u.Username
		} else {
			by = "cli"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	r, err := db.New(conn).PurgeExpired(ctx, db.PurgeExpiredParams{
		Retention: pgtype.Interval{Months: int32(months), Valid: true}, DryRun: dryRun, RequestedBy: by})
	if err != nil {
		return err
	}
	verb := "deleted"
	if r.DryRun {
		verb = "would delete (dry run; DRY_RUN=0 / -dry-run=false to delete)"
	}
	fmt.Printf("retention %d months, cutoff %s\n", months, r.Cutoff.UTC().Format(time.RFC3339))
	fmt.Printf("%s: %d messages, %d entity_sightings, %d summaries\n", verb, r.MessagesDeleted, r.SightingsDeleted, r.SummariesDeleted)
	fmt.Printf("audit: retention_purges id %d, requested_by %q, db_user %q\n", r.ID, r.RequestedBy, r.DbUser)
	return nil
}
