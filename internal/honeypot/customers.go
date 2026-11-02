package honeypot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/JaxzTan/Buru_AI/internal/customerapi"
	"github.com/JaxzTan/Buru_AI/internal/db"
)

// Bank customers and their API keys (FR-10, FR-13, TR-13; TRD G-7). POST /v1/lookup accepts a
// customer key in addition to the shared operator token; lookups made with a key are logged in
// lookup_logs (never the looked-up value).

// APIKeyView is an api_keys row without key_hash.
type APIKeyView struct {
	ID              int64      `json:"id"`
	CustomerID      int64      `json:"customer_id"`
	KeyPrefix       string     `json:"key_prefix"`
	Status          string     `json:"status"`
	RateLimitPerMin int32      `json:"rate_limit_per_min"`
	CreatedAt       time.Time  `json:"created_at"`
	RevokedAt       *time.Time `json:"revoked_at"`
	Lookups         *int64     `json:"lookups,omitempty"`
}

func apiKeyView(k db.ApiKey) APIKeyView {
	return APIKeyView{ID: k.ID, CustomerID: k.CustomerID, KeyPrefix: k.KeyPrefix, Status: string(k.Status),
		RateLimitPerMin: k.RateLimitPerMin, CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt}
}

// IssuedKey is returned once, when the key is created. Key is never stored or shown again.
type IssuedKey struct {
	Key    string     `json:"key"`
	APIKey APIKeyView `json:"api_key"`
}

type CustomerInput struct {
	Name string `json:"name"`
	Type string `json:"type"` // bank | ewallet | exchange
}

func (s *Service) CreateCustomer(ctx context.Context, in CustomerInput) (db.Customer, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 255 {
		return db.Customer{}, fmt.Errorf("%w: name is required (max 255 chars)", ErrBadRequest)
	}
	t := db.CustomerType(strings.ToLower(strings.TrimSpace(in.Type)))
	if in.Type == "" {
		t = db.CustomerTypeBank
	}
	if !t.Valid() {
		return db.Customer{}, fmt.Errorf("%w: type must be bank, ewallet or exchange", ErrBadRequest)
	}
	return s.Q.CreateCustomer(ctx, db.CreateCustomerParams{Name: name, Type: t})
}

func (s *Service) Customers(ctx context.Context) ([]db.Customer, error) {
	return s.Q.ListCustomers(ctx)
}

func (s *Service) IssueAPIKey(ctx context.Context, customerID int64, ratePerMin int32) (IssuedKey, error) {
	if _, err := s.Q.GetCustomer(ctx, customerID); err != nil {
		return IssuedKey{}, notFound(err)
	}
	if ratePerMin == 0 {
		ratePerMin = 600
	}
	if ratePerMin < 1 || ratePerMin > 100000 {
		return IssuedKey{}, fmt.Errorf("%w: rate_limit_per_min must be 1-100000", ErrBadRequest)
	}
	raw, prefix, hash, err := customerapi.NewKey()
	if err != nil {
		return IssuedKey{}, err
	}
	k, err := s.Q.CreateAPIKey(ctx, db.CreateAPIKeyParams{CustomerID: customerID, KeyPrefix: prefix, KeyHash: hash, RateLimitPerMin: ratePerMin})
	if err != nil {
		return IssuedKey{}, err
	}
	return IssuedKey{Key: raw, APIKey: apiKeyView(k)}, nil
}

func (s *Service) APIKeys(ctx context.Context, customerID int64) ([]APIKeyView, error) {
	if _, err := s.Q.GetCustomer(ctx, customerID); err != nil {
		return nil, notFound(err)
	}
	rows, err := s.Q.ListAPIKeys(ctx, customerID)
	if err != nil {
		return nil, err
	}
	out := make([]APIKeyView, 0, len(rows))
	for _, k := range rows {
		v := apiKeyView(k)
		n, err := s.Q.CountLookupLogsForKey(ctx, k.ID)
		if err != nil {
			return nil, err
		}
		v.Lookups = &n
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) RevokeAPIKey(ctx context.Context, customerID, keyID int64) (APIKeyView, error) {
	k, err := s.Q.RevokeAPIKey(ctx, db.RevokeAPIKeyParams{ID: keyID, CustomerID: customerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKeyView{}, fmt.Errorf("%w: no active key %d for customer %d", ErrNotFound, keyID, customerID)
	}
	if err != nil {
		return APIKeyView{}, err
	}
	return apiKeyView(k), nil
}

// AuthenticateAPIKey returns the active key for a raw bearer token. ok is false for an unknown or
// revoked key.
func (s *Service) AuthenticateAPIKey(ctx context.Context, raw string) (db.ApiKey, bool, error) {
	if !customerapi.LooksLikeKey(raw) {
		return db.ApiKey{}, false, nil
	}
	k, err := s.Q.GetActiveAPIKeyByHash(ctx, customerapi.Hash(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ApiKey{}, false, nil
	}
	return k, err == nil, err
}

// LogLookup appends a lookup_logs row (FR-13): type, matched, latency; never the value.
func (s *Service) LogLookup(ctx context.Context, k db.ApiKey, entityType string, matched bool, latency time.Duration) error {
	t := db.EntityType(entityType)
	if !t.Valid() {
		return nil // rejected with 400 before any lookup happened
	}
	return s.Q.InsertLookupLog(ctx, db.InsertLookupLogParams{CustomerID: k.CustomerID, ApiKeyID: k.ID, EntityType: t,
		Matched: matched, LatencyMs: int32(min(latency.Milliseconds(), 1<<31-1))})
}

type UsageView struct {
	CustomerID int64                       `json:"customer_id"`
	Months     []db.MonthlyLookupCountsRow `json:"months"`
	Recent     []db.LookupLog              `json:"recent"`
}

func (s *Service) Usage(ctx context.Context, customerID int64) (UsageView, error) {
	if _, err := s.Q.GetCustomer(ctx, customerID); err != nil {
		return UsageView{}, notFound(err)
	}
	months, err := s.Q.MonthlyLookupCounts(ctx, db.MonthlyLookupCountsParams{CustomerID: customerID, Since: s.Now().AddDate(0, -12, 0)})
	if err != nil {
		return UsageView{}, err
	}
	recent, err := s.Q.ListLookupLogs(ctx, db.ListLookupLogsParams{CustomerID: customerID, Limit: 50})
	if err != nil {
		return UsageView{}, err
	}
	return UsageView{CustomerID: customerID, Months: months, Recent: recent}, nil
}
