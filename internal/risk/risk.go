// Package risk turns a lookup hit into an explained, rule-based recommendation for a bank: a score,
// a reason code, the signals behind it and a recommended action. It never recommends a freeze: the
// bank's own reviewer decides (spec: "recommend, don't auto-freeze"). Pure function, no I/O, so the
// live API (internal/honeypot) and the demo server (internal/server/demo.go) give the same answer.
// web/src/lib/risk.ts is a line-for-line port for the scripted bank story; keep the two in step.
package risk

import "math"

// Reason codes.
const (
	ReasonAccount = "ACCOUNT_SUPPLIED_BY_SCAMMER" // bank_account, wallet
	ReasonContact = "CONTACT_USED_BY_SCAMMER"     // phone, handle
	ReasonLink    = "LINK_SHARED_BY_SCAMMER"      // url
)

// Signals.
const (
	SignalVerified = "OPERATOR_VERIFIED"
	SignalRepeat   = "REPEAT_SIGHTINGS"
)

// Recommended actions, strongest first.
const (
	ActionHold    = "HOLD_FOR_REVIEW"
	ActionWarn    = "WARN_CUSTOMER"
	ActionMonitor = "MONITOR"
)

type Result struct {
	Score   float64  `json:"risk_score"`
	Reason  string   `json:"reason"`
	Signals []string `json:"signals"`
	Action  string   `json:"recommended_action"`
}

// Assess scores a matched entity. confidence is the value the lookup returns (already "medium" for an
// unverified entity, FR-9). Base by confidence (high 0.90, medium 0.70, low 0.50) plus 0.02 per extra
// sighting up to +0.08, capped at 0.99. Hold needs an operator-verified record scoring 0.85 or more.
func Assess(typ, status, confidence string, sightings int32) Result {
	base := 0.50
	switch confidence {
	case "high":
		base = 0.90
	case "medium":
		base = 0.70
	}
	extra := 0.0
	if sightings > 1 {
		extra = math.Min(0.02*float64(sightings-1), 0.08)
	}
	score := math.Round(math.Min(base+extra, 0.99)*100) / 100

	r := Result{Score: score, Signals: []string{}}
	switch typ {
	case "phone", "handle":
		r.Reason = ReasonContact
	case "url":
		r.Reason = ReasonLink
	default:
		r.Reason = ReasonAccount
	}
	verified := status == "verified"
	if verified {
		r.Signals = append(r.Signals, SignalVerified)
	}
	if sightings > 1 {
		r.Signals = append(r.Signals, SignalRepeat)
	}
	switch {
	case verified && score >= 0.85:
		r.Action = ActionHold
	case score >= 0.60:
		r.Action = ActionWarn
	default:
		r.Action = ActionMonitor
	}
	return r
}
