// Command customerkey creates a bank customer and issues one API key for POST /v1/lookup (TR-13).
// The raw key is printed once; only its hash is stored.
//
//	go run ./cmd/customerkey -db 'postgres://buru:buru@localhost:15432/sockabus_e2e?sslmode=disable' -name 'Demo Bank'
//	make demo-customer DB='postgres://...' NAME='Demo Bank'
//
// It refuses a Supabase (production) URL unless -allow-production is given: create production
// customers deliberately, not by running a demo helper against the wrong database.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JaxzTan/Buru_AI/internal/customerapi"
	"github.com/JaxzTan/Buru_AI/internal/db"
)

func main() {
	url := flag.String("db", "", "PostgreSQL URL (required; never defaults to .env)")
	name := flag.String("name", "Demo Bank", "customer name")
	typ := flag.String("type", "bank", "bank | ewallet | exchange")
	rate := flag.Int("rate", 600, "rate_limit_per_min for the key")
	customerID := flag.Int64("customer", 0, "issue a key for this existing customer id instead of creating one")
	prod := flag.Bool("allow-production", false, "allow a Supabase URL")
	flag.Parse()
	if err := run(*url, *name, *typ, int32(*rate), *customerID, *prod); err != nil {
		fmt.Fprintln(os.Stderr, "customerkey:", err)
		os.Exit(1)
	}
}

func run(url, name, typ string, rate int32, customerID int64, prod bool) error {
	if url == "" {
		return fmt.Errorf("-db is required")
	}
	if strings.Contains(url, "supabase") && !prod {
		return fmt.Errorf("refusing a Supabase (production) URL without -allow-production")
	}
	ct := db.CustomerType(typ)
	if !ct.Valid() {
		return fmt.Errorf("-type must be bank, ewallet or exchange")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := db.New(pool)
	if customerID == 0 {
		c, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Name: name, Type: ct})
		if err != nil {
			return err
		}
		customerID = c.ID
		fmt.Printf("customer %d created: %s (%s)\n", c.ID, c.Name, c.Type)
	} else if _, err := q.GetCustomer(ctx, customerID); err != nil {
		return fmt.Errorf("customer %d: %w", customerID, err)
	}
	raw, prefix, hash, err := customerapi.NewKey()
	if err != nil {
		return err
	}
	k, err := q.CreateAPIKey(ctx, db.CreateAPIKeyParams{CustomerID: customerID, KeyPrefix: prefix, KeyHash: hash, RateLimitPerMin: rate})
	if err != nil {
		return err
	}
	fmt.Printf("api key %d (prefix %s, %d/min). Shown once, store it now:\n%s\n", k.ID, k.KeyPrefix, k.RateLimitPerMin, raw)
	fmt.Printf("try: curl -s -H 'Authorization: Bearer %s' -d '{\"type\":\"bank_account\",\"value\":\"1234567890\",\"bank\":\"MBB\"}' http://localhost:8080/v1/lookup\n", raw)
	return nil
}
