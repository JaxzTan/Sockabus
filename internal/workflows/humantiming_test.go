package workflows

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// simulateReplies runs n replies through the planner over realistic chats: conversations start at
// random times over a week, the scammer writes 20–140 chars (half are questions), the persona
// answers with 1–3 bubbles of 8–60 chars, and the scammer's next message follows the persona's
// last bubble: 80% within the chat (lognormal, median 30 s), 20% much later (median 6 h, so those
// land at any hour, sleep window included). FR-3's 10 s floor applies. It returns the share of replies
// whose first bubble landed within 1 and 5 minutes of the scammer's message.
func simulateReplies(p PersonaTiming, n int, seed int64) (under1, under5 float64) {
	r := rand.New(rand.NewSource(seed))
	p = p.WithDefaults()
	base := time.Date(2026, 11, 2, 0, 0, 0, 0, myt)
	var u1, u5 int
	const perConv = 10
	for done := 0; done < n; {
		at := base.Add(time.Duration(r.Int63n(int64(7 * 24 * time.Hour))))
		since := time.Duration(-1) // first message: nothing exchanged yet
		for i := 0; i < perConv && done < n; i++ {
			incoming := strings.Repeat("x", 20+r.Intn(120))
			if r.Intn(2) == 0 {
				incoming += "?"
			}
			bubbles := make([]string, 1+r.Intn(3))
			for j := range bubbles {
				bubbles[j] = strings.Repeat("y", 8+r.Intn(52))
			}
			plan := p.PlanReply(r, at, since, incoming, bubbles, MinReplyDelay)
			if plan.FirstSendAt < time.Minute {
				u1++
			}
			if plan.FirstSendAt < 5*time.Minute {
				u5++
			}
			done++
			last := plan.FirstSendAt
			for j := 1; j < len(bubbles); j++ {
				last += plan.GapBefore[j] + plan.Typing[j]
			}
			if r.Float64() < 0.8 {
				since = secs(lognormal(r, 30, 1.0))
			} else {
				since = secs(lognormal(r, 6*3600, 1.0))
			}
			at = at.Add(last + since)
		}
	}
	return float64(u1) / float64(n), float64(u5) / float64(n)
}

func TestPlannerDistribution(t *testing.T) {
	u1, u5 := simulateReplies(PersonaTiming{}, 10000, 1)
	t.Logf("default persona: %.1f%% < 1 min, %.1f%% < 5 min", u1*100, u5*100)
	if u1 < 0.55 || u1 > 0.61 {
		t.Errorf("%.1f%% of replies under 1 min, want ~58%%", u1*100)
	}
	if u5 < 0.66 || u5 > 0.72 {
		t.Errorf("%.1f%% of replies under 5 min, want ~69%%", u5*100)
	}
	// The seeded personas, for reference (slower/older personas skew later).
	for name, p := range map[string]PersonaTiming{
		"Auntie Mei": {WPM: 23, Sleep: []int{22, 6}, Busyness: 0.2, TypoRate: 0.12},
		"Ah Seng":    {WPM: 35, Sleep: []int{2, 9}, Busyness: 0.3, TypoRate: 0.04},
		"Kak Ros":    {WPM: 28, Sleep: []int{23, 5}, Busyness: 0.6, TypoRate: 0.06},
		"Uncle Wong": {WPM: 22, Sleep: []int{22, 5}, Busyness: 0.25, TypoRate: 0.10},
	} {
		u1, u5 := simulateReplies(p, 10000, 1)
		t.Logf("%-10s: %.1f%% < 1 min, %.1f%% < 5 min", name, u1*100, u5*100)
	}
}

func TestParsePersonaTiming(t *testing.T) {
	got := ParsePersonaTiming([]byte(`{"city":"Ipoh","timing":{"wpm":22,"sleep":[22,5],"busyness":0.25,"typo_rate":0.1}}`))
	if got.WPM != 22 || got.Sleep[0] != 22 || got.Sleep[1] != 5 || got.Busyness != 0.25 || got.TypoRate != 0.1 {
		t.Fatalf("parsed %+v", got)
	}
	for _, raw := range []string{``, `null`, `{}`, `{"timing":{"sleep":[30]}}`} {
		d := ParsePersonaTiming([]byte(raw))
		if d.WPM != DefaultWPM || d.Busyness != DefaultBusyness || d.TypoRate != DefaultTypoRate || len(d.Sleep) != 2 {
			t.Errorf("%q: defaults %+v", raw, d)
		}
	}
}

func TestAsleepWrapsMidnight(t *testing.T) {
	p := PersonaTiming{Sleep: []int{22, 6}}.WithDefaults()
	for h, want := range map[int]bool{21: false, 22: true, 23: true, 0: true, 5: true, 6: false, 14: false} {
		if got := p.Asleep(time.Date(2026, 11, 4, h, 30, 0, 0, myt)); got != want {
			t.Errorf("hour %d: asleep = %v, want %v", h, got, want)
		}
	}
	at := time.Date(2026, 11, 4, 23, 0, 0, 0, myt)
	if w := p.wake(at); !w.Equal(time.Date(2026, 11, 5, 6, 0, 0, 0, myt)) {
		t.Errorf("wake = %v", w)
	}
}

func TestTypingTimeFollowsWPM(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	slow, fast := PersonaTiming{WPM: 22}.WithDefaults(), PersonaTiming{WPM: 35}.WithDefaults()
	msg := strings.Repeat("a", 50) // 50 chars at 22 WPM (1.83 cps) ≈ 27 s ± 20%
	if d := slow.TypingTime(r, msg); d < 21*time.Second || d > 33*time.Second {
		t.Errorf("22 WPM typing = %v", d)
	}
	if d := fast.TypingTime(r, msg); d < 13*time.Second || d > 21*time.Second {
		t.Errorf("35 WPM typing = %v", d)
	}
}
