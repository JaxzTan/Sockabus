package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Per-client rate limits (token buckets, golang.org/x/time/rate). Every request takes a token from
// the global bucket; some routes also take one from a stricter tier. Limits are per client IP and
// per process (one Go app per deployment), so no Redis round-trip is needed.
type tier struct {
	name  string
	every time.Duration // one token per `every`
	burst int
}

var (
	tierGlobal = tier{"global", time.Minute / 1200, 200} // console polls ~4 lists every 3 s per tab
	tierAuth   = tier{"auth", time.Minute / 30, 15}      // sign-in allowlist check
	tierLLM    = tier{"llm", time.Minute / 20, 5}        // routes that call the LLM provider (cost)
	tierGen    = tier{"generate", time.Minute / 5, 2}    // persona generation: long, expensive calls
	tierLookup = tier{"lookup", time.Minute / 300, 60}   // customer lookup API
	tierBulk   = tier{"bulk", time.Minute / 300, 60}     // list endpoints that return many records
)

// routeTier picks the stricter tier for a request, or nil when only the global limit applies.
func routeTier(r *http.Request) *tier {
	p := r.URL.Path
	switch {
	case p == "/internal/auth/google/check":
		return &tierAuth
	case r.Method == http.MethodPost && p == "/internal/personas/generate":
		return &tierGen
	case r.Method == http.MethodPost && (strings.HasSuffix(p, "/summary") || p == "/internal/extract" ||
		p == "/internal/sim/messages" || strings.HasSuffix(p, "/inbound")):
		return &tierLLM
	case p == "/v1/lookup":
		return &tierLookup
	case r.Method == http.MethodGet && (p == "/internal/conversations" || p == "/internal/entities" ||
		p == "/internal/customers" || p == "/internal/personas" || p == "/internal/accounts" ||
		p == "/v1/entities" || p == "/v1/weeks" || p == "/v1/usage" || strings.HasSuffix(p, "/usage") || strings.HasSuffix(p, "/keys")):
		return &tierBulk
	}
	return nil
}

type bucket struct {
	l    *rate.Limiter
	seen time.Time
}

type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: map[string]*bucket{}, lastSweep: time.Now()}
}

// allow takes a token for key from tier t. It returns how long to wait when there is none.
func (rl *rateLimiter) allow(t tier, key string, now time.Time) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if now.Sub(rl.lastSweep) > time.Minute { // drop idle clients so the map can't grow without bound
		for k, b := range rl.buckets {
			if now.Sub(b.seen) > 10*time.Minute {
				delete(rl.buckets, k)
			}
		}
		rl.lastSweep = now
	}
	k := t.name + "|" + key
	b, ok := rl.buckets[k]
	if !ok {
		b = &bucket{l: rate.NewLimiter(rate.Every(t.every), t.burst)}
		rl.buckets[k] = b
	}
	b.seen = now
	res := b.l.ReserveN(now, 1)
	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now)
		return false, d
	}
	return true, 0
}

// middleware answers 429 with Retry-After once a client is over a limit. /healthz is exempt so
// uptime checks never trip it.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		key, now := clientIP(r), time.Now()
		tiers := []tier{tierGlobal}
		if t := routeTier(r); t != nil {
			tiers = append(tiers, *t)
		}
		for _, t := range tiers {
			if ok, wait := rl.allow(t, key, now); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(retrySeconds(wait)))
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests, retry later", "code": "rate_limited"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the address a limit is counted against. Requests from a private or loopback peer
// (Caddy, the Next.js /api proxy, or another container) use the right-most X-Forwarded-For entry,
// the one that proxy appended; a direct internet client can't choose its own key that way.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if fwd := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); fwd != nil {
				return fwd.String()
			}
		}
	}
	return host
}

// cors answers cross-origin browser requests only for the configured origins (SOCKABUS_CORS_ORIGINS).
// With none configured no CORS header is ever sent, so browsers keep the same-origin default; the
// console reaches the API through its own /api proxy and bank clients call server-to-server.
func cors(origins []string, next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range origins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" && o != "*" {
			allowed[o] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		w.Header().Add("Vary", "Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		} else if origin != "" && r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.WriteHeader(http.StatusForbidden) // preflight from an origin that isn't allowed
			return
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets headers every API response should carry: JSON is never sniffed as HTML,
// framed, or cached by a shared cache.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
