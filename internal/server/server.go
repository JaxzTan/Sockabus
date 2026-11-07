// Package server wires the HTTP routes for the customer API, operator API and health checks.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/JaxzTan/Buru_AI/internal/channel/sim"
	"github.com/JaxzTan/Buru_AI/internal/demostore"
	"github.com/JaxzTan/Buru_AI/internal/engine"
	"github.com/JaxzTan/Buru_AI/internal/honeypot"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Server struct {
	db    *pgxpool.Pool
	redis *redis.Client
	demo  *demostore.Store // optional: live demo data
	eng   *engine.Engine
	live  *liveAPI // SOCKABUS_MODE=live: PostgreSQL-backed API (takes precedence over demo)

	demoToken string // guards POST /internal/auth/google/check in demo mode; empty refuses every call

	temporal func(context.Context) error // readyz check; nil skips it

	corsOrigins []string // SOCKABUS_CORS_ORIGINS; empty sends no CORS headers
	noRateLimit bool     // SOCKABUS_RATE_LIMIT=off (load tests only)
}

// WithCORS allows cross-origin browser calls from exactly these origins ("*" is ignored).
func (s *Server) WithCORS(origins []string) *Server {
	s.corsOrigins = origins
	return s
}

// WithoutRateLimit turns the per-client rate limits off. For load tests only.
func (s *Server) WithoutRateLimit() *Server {
	s.noRateLimit = true
	return s
}

// WithLive serves the PostgreSQL-backed operator API (/internal/*) and lookup (/v1/lookup).
// operatorToken guards every route; operatorID is recorded on prompt versions, operator
// messages and verifications until operator login (email + TOTP, TRD §7) exists.
func (s *Server) WithLive(svc *honeypot.Service, connect honeypot.AccountConnector, operatorToken string, operatorID int64) *Server {
	s.live = &liveAPI{svc: svc, connect: connect, token: operatorToken, operatorID: operatorID}
	return s
}

// WithSim mounts the development chat simulator (/internal/sim/*). Call after WithLive.
func (s *Server) WithSim(hub *sim.Hub) *Server {
	if s.live != nil {
		s.live.sim = hub
	}
	return s
}

// WithStatus sets what GET /internal/status reports. Call after WithLive.
func (s *Server) WithStatus(src StatusSources) *Server {
	if s.live != nil {
		s.live.src = src
	}
	return s
}

// WithTemporalCheck adds Temporal to /readyz.
func (s *Server) WithTemporalCheck(check func(context.Context) error) *Server {
	s.temporal = check
	return s
}

func New(db *pgxpool.Pool, rdb *redis.Client) *Server {
	return &Server{db: db, redis: rdb}
}

// WithDemo enables the console + portal APIs, and starts the scripted scammer simulator
// (set SOCKABUS_SIMULATE=0 to disable it).
func (s *Server) WithDemo(st *demostore.Store) *Server {
	s.demo = st
	s.eng = engine.New(st)
	if os.Getenv("SOCKABUS_SIMULATE") != "0" {
		go s.eng.RunSimulator(context.Background())
	}
	return s
}

// WithOperatorToken sets the bearer token demo mode requires on POST /internal/auth/google/check
// (the console's Google sign-in allowlist). Live mode uses the token given to WithLive.
func (s *Server) WithOperatorToken(token string) *Server {
	s.demoToken = token
	return s
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	switch {
	case s.live != nil:
		s.live.register(mux)
	case s.demo != nil:
		demoMux := http.NewServeMux()
		s.registerDemo(demoMux)
		if s.demoToken != "" {
			// With an operator token set, demo mode guards its API like live mode does: the Next.js
			// /api proxy adds the token, so only the signed-in console (and holders of the token) get in.
			mux.Handle("/v1/", (&liveAPI{token: s.demoToken}).auth(demoMux.ServeHTTP))
			mux.Handle("/internal/", (&liveAPI{token: s.demoToken}).auth(demoMux.ServeHTTP))
		} else {
			slog.Warn("demo mode without SOCKABUS_OPERATOR_TOKEN: /v1/* and /internal/* are unauthenticated (local use only)")
			mux.Handle("/v1/", demoMux)
			mux.Handle("/internal/", demoMux)
		}
		mux.Handle("POST /internal/auth/google/check", (&liveAPI{token: s.demoToken}).auth(googleCheck))
	}
	var h http.Handler = mux
	if !s.noRateLimit {
		h = newRateLimiter().middleware(h)
	}
	return securityHeaders(cors(s.corsOrigins, h))
}

// healthz reports that the process is alive. It checks no dependencies, so a database or
// Temporal outage never gets the process restarted; /readyz covers those.
func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz reports whether downstream dependencies are reachable (TR-16).
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]string{"postgres": "ok", "redis": "ok"}
	status := http.StatusOK
	// /readyz is public (Caddyfile): report only "unavailable", never the error, which can carry
	// hostnames, ports or connection-string details. The cause goes to the server log.
	down := func(name string, err error) {
		slog.Warn("readyz: dependency unavailable", "dependency", name, "err", err)
		checks[name] = "unavailable"
		status = http.StatusServiceUnavailable
	}
	if err := s.db.Ping(ctx); err != nil {
		down("postgres", err)
	}
	if err := s.redis.Ping(ctx).Err(); err != nil {
		down("redis", err)
	}
	if s.temporal != nil {
		checks["temporal"] = "ok"
		if err := s.temporal(ctx); err != nil {
			down("temporal", err)
		}
	}
	writeJSON(w, status, checks)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
