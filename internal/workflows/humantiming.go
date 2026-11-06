package workflows

import (
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"time"
)

// Human-like reply timing (per persona). A reply's latency is
//
//	offline gate (asleep / away) + notice + read + think + type (per bubble) + bubble gaps
//
// Timing lives here, in the orchestrator; the prompt only shapes the content. Calibrated so that,
// over a realistic chat (TestPlannerDistribution), ~58% of replies land within 1 min and ~69%
// within 5 min, with composing speed 22–35 WPM (slower for older personas).
//
// Every function takes its random source as an argument: the workflow calls them inside
// workflow.SideEffect so replays reuse the recorded values.

// PersonaTiming is a persona's timing profile, stored in personas.profile under "timing":
//
//	"timing": {"wpm": 23, "sleep": [22, 6], "busyness": 0.2, "typo_rate": 0.12}
//
// Zero values take defaults (see WithDefaults).
type PersonaTiming struct {
	WPM      float64 `json:"wpm,omitempty"`       // composing speed, words per minute
	Sleep    []int   `json:"sleep,omitempty"`     // [startHour, endHour] Malaysia time; may wrap midnight
	Busyness float64 `json:"busyness,omitempty"`  // 0–1: how long they take to notice a message when not chatting
	TypoRate float64 `json:"typo_rate,omitempty"` // probability a bubble gets one adjacent-key typo
}

// Defaults for a persona without a "timing" profile.
const (
	DefaultWPM      = 30
	DefaultBusyness = 0.3
	DefaultTypoRate = 0.05

	activeWindow = 2 * time.Minute // a message within this of the last exchange is seen almost at once
	// Chance the persona is pulled away (phone face down, cooking, driving) before noticing.
	// The reference algorithm's 8% left too few replies past 5 min; 22% calibrates to ~69%.
	distractedChance  = 0.22
	abandonTypingRate = 0.5 // chance a new scammer message mid-typing makes the persona start over
)

var defaultSleep = []int{23, 7}

var myt = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Kuala_Lumpur"); err == nil {
		return loc
	}
	return time.FixedZone("MYT", 8*3600)
}()

func (p PersonaTiming) WithDefaults() PersonaTiming {
	if p.WPM <= 0 {
		p.WPM = DefaultWPM
	}
	if len(p.Sleep) != 2 || p.Sleep[0] < 0 || p.Sleep[0] > 23 || p.Sleep[1] < 0 || p.Sleep[1] > 23 {
		p.Sleep = defaultSleep
	}
	if p.Busyness <= 0 || p.Busyness > 1 {
		p.Busyness = DefaultBusyness
	}
	if p.TypoRate <= 0 || p.TypoRate > 1 {
		p.TypoRate = DefaultTypoRate
	}
	return p
}

// ParsePersonaTiming reads the "timing" key of a personas.profile document. Missing or malformed
// values fall back to defaults.
func ParsePersonaTiming(profile json.RawMessage) PersonaTiming {
	var doc struct {
		Timing PersonaTiming `json:"timing"`
	}
	_ = json.Unmarshal(profile, &doc)
	return doc.Timing.WithDefaults()
}

// lognormal returns median·e^(σz), z standard normal.
func lognormal(r *rand.Rand, median, sigma float64) float64 {
	return median * math.Exp(sigma*r.NormFloat64())
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// Asleep reports whether t falls in the persona's sleep window (Malaysia time).
func (p PersonaTiming) Asleep(t time.Time) bool {
	h, start, end := t.In(myt).Hour(), p.Sleep[0], p.Sleep[1]
	if start == end {
		return false
	}
	if start < end {
		return h >= start && h < end
	}
	return h >= start || h < end // wraps midnight, e.g. [22, 6]
}

// wake returns the next end of the sleep window after t.
func (p PersonaTiming) wake(t time.Time) time.Time {
	l := t.In(myt)
	w := time.Date(l.Year(), l.Month(), l.Day(), p.Sleep[1], 0, 0, 0, myt)
	if !w.After(l) {
		w = w.AddDate(0, 0, 1)
	}
	return w
}

// NoticeDelay is how long after a message arrives at `at` the persona opens the chat: the offline
// gate (asleep → wake + ~30 min) plus the notice time, which is short while the chat is active
// (last exchange under 2 min ago) and minutes otherwise; sometimes they get distracted.
func (p PersonaTiming) NoticeDelay(r *rand.Rand, at time.Time, sinceExchange time.Duration) time.Duration {
	if p.Asleep(at) {
		return p.wake(at).Sub(at) + secs(lognormal(r, 1800, 0.8))
	}
	var n float64
	if sinceExchange >= 0 && sinceExchange < activeWindow {
		n = lognormal(r, 4, 0.6)
	} else {
		n = lognormal(r, 45+120*p.Busyness, 1.3)
	}
	if r.Float64() < distractedChance {
		n += lognormal(r, 900, 0.7)
	}
	return secs(n)
}

// ReadThink is reading the incoming burst (1 s + 25 chars/s) plus thinking about an answer
// (longer when they asked a question).
func ReadThink(r *rand.Rand, chars int, question bool) time.Duration {
	think := 2.0
	if question {
		think = 4
	}
	return secs(1 + float64(chars)/25 + lognormal(r, think, 0.7))
}

// TypingTime is how long the persona shows "typing…" for one bubble: chars / (wpm·5/60) ± 20%.
func (p PersonaTiming) TypingTime(r *rand.Rand, bubble string) time.Duration {
	cps := p.WPM * 5 / 60
	return secs(float64(len([]rune(bubble))) / cps * (0.8 + 0.4*r.Float64()))
}

// BubbleGap is the pause before each bubble after the first.
func BubbleGap(r *rand.Rand) time.Duration { return secs(lognormal(r, 1.5, 0.5)) }

// QuietGap is the debounce: wait this long after the scammer's latest message before replying.
func QuietGap(r *rand.Rand) time.Duration { return secs(3 + 5*r.Float64()) }

// AwayGap is how long a persona that said "brb" stays away (~25 min).
func AwayGap(r *rand.Rand) time.Duration { return secs(lognormal(r, 1500, 0.25)) }

// ReplyPlan is the whole schedule for one reply, relative to the scammer's latest message.
type ReplyPlan struct {
	ReadAt        time.Duration   // read receipt
	FirstTypingAt time.Duration   // "typing…" starts for the first bubble
	Typing        []time.Duration // per bubble
	GapBefore     []time.Duration // per bubble; 0 for the first
	FirstSendAt   time.Duration   // first bubble lands (FR-3 floor applied)
}

// PlanReply composes the pieces the workflow uses for one reply to a single message `incoming`
// arriving at `at`. The workflow runs the same steps one at a time so it can react to new
// messages in between; this is used by the calibration test.
func (p PersonaTiming) PlanReply(r *rand.Rand, at time.Time, sinceExchange time.Duration, incoming string, bubbles []string, floor time.Duration) ReplyPlan {
	var plan ReplyPlan
	plan.ReadAt = max(p.NoticeDelay(r, at, sinceExchange), QuietGap(r))
	plan.FirstTypingAt = plan.ReadAt + ReadThink(r, len([]rune(incoming)), strings.Contains(incoming, "?"))
	for i, b := range bubbles {
		gap := time.Duration(0)
		if i > 0 {
			gap = BubbleGap(r)
		}
		plan.GapBefore = append(plan.GapBefore, gap)
		plan.Typing = append(plan.Typing, p.TypingTime(r, b))
	}
	plan.FirstSendAt = plan.FirstTypingAt
	if len(plan.Typing) > 0 {
		plan.FirstSendAt += plan.Typing[0]
	}
	plan.FirstSendAt = max(plan.FirstSendAt, floor)
	return plan
}
