package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrUpstream is matched by every *UpstreamError (errors.Is(err, llm.ErrUpstream)).
var ErrUpstream = errors.New("llm: upstream provider error")

// Kind classifies an upstream failure so callers (retry, HTTP status mapping) never parse strings.
type Kind string

const (
	KindRateLimited Kind = "rate_limited" // HTTP 429
	KindOverloaded  Kind = "overloaded"   // HTTP 529 (Anthropic) / 503 RESOURCE_EXHAUSTED-style overload
	KindUnavailable Kind = "unavailable"  // HTTP 5xx, dropped connection
	KindTimeout     Kind = "timeout"      // HTTP 408/504 or our own deadline
	KindAuth        Kind = "auth"         // HTTP 401/403: key missing, wrong or not allowed
	KindBilling     Kind = "billing"      // HTTP 402
	KindBadRequest  Kind = "bad_request"  // HTTP 400/404/413/422: our request is wrong (model id, params)
	KindRefused     Kind = "refused"      // HTTP 200 with stop_reason=refusal (see also ErrBlocked)
	KindTruncated   Kind = "truncated"    // HTTP 200 but the output hit max_tokens before any text
)

// UpstreamError is a failed call to the LLM provider.
type UpstreamError struct {
	Provider   string        // "anthropic" | "gemini"
	Status     int           // HTTP status; 0 when no response arrived
	Kind       Kind          //
	RetryAfter time.Duration // from the retry-after header; 0 when absent
	Message    string        // provider's error message (truncated), never the API key
	Err        error         // underlying error (e.g. context.DeadlineExceeded), may be nil
}

func (e *UpstreamError) Error() string {
	var b strings.Builder
	b.WriteString(e.Provider)
	if e.Status != 0 {
		fmt.Fprintf(&b, ": HTTP %d", e.Status)
	}
	fmt.Fprintf(&b, " (%s)", e.Kind)
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.Err != nil && e.Message == "" {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

// Unwrap lets errors.Is match both ErrUpstream and the cause (context.DeadlineExceeded, ErrBlocked).
func (e *UpstreamError) Unwrap() []error {
	if e.Err != nil {
		return []error{ErrUpstream, e.Err}
	}
	return []error{ErrUpstream}
}

// Temporary reports whether retrying the same request later can succeed.
func (e *UpstreamError) Temporary() bool {
	switch e.Kind {
	case KindRateLimited, KindOverloaded, KindUnavailable, KindTimeout:
		return true
	}
	return false
}

// AsUpstream returns the *UpstreamError in err's chain, if any.
func AsUpstream(err error) (*UpstreamError, bool) {
	var u *UpstreamError
	ok := errors.As(err, &u)
	return u, ok
}

// kindForStatus maps an HTTP status from the provider to a Kind.
func kindForStatus(status int) Kind {
	switch {
	case status == http.StatusTooManyRequests:
		return KindRateLimited
	case status == 529:
		return KindOverloaded
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return KindTimeout
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return KindAuth
	case status == http.StatusPaymentRequired:
		return KindBilling
	case status >= 500:
		return KindUnavailable
	default:
		return KindBadRequest
	}
}

// httpError builds the UpstreamError for a non-200 response.
func httpError(provider string, res *http.Response, msg string) *UpstreamError {
	return &UpstreamError{Provider: provider, Status: res.StatusCode, Kind: kindForStatus(res.StatusCode),
		RetryAfter: parseRetryAfter(res.Header.Get("retry-after")), Message: msg}
}

// transportError wraps a failed http.Do: our deadline → timeout, anything else → unavailable.
// A cancelled caller context is returned as is (not an upstream problem).
func transportError(provider string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, context.DeadlineExceeded):
		return &UpstreamError{Provider: provider, Kind: KindTimeout, Err: context.DeadlineExceeded}
	default:
		return &UpstreamError{Provider: provider, Kind: KindUnavailable, Err: err}
	}
}

// parseRetryAfter accepts delta-seconds (what both providers send) or an HTTP date.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
