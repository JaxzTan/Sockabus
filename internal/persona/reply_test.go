package persona

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JaxzTan/Buru_AI/internal/playbook"
)

func TestParseReply(t *testing.T) {
	cases := []struct{ in, action, text string }{
		{`{"action":"reply","bubbles":["wah really ah","how to join?"]}`, "reply", "wah really ah\n\nhow to join?"},
		{"```json\n{\"action\":\"brb\",\"bubbles\":[\"wait ah cooking\"]}\n```", "brb", "wait ah cooking"},
		{`{"action":"ignore","bubbles":[]}`, "ignore", ""},
		{`sure: {"action":"REPLY","bubbles":[" ok la ",""]}`, "reply", "ok la"},
		{`{"action":"dance","bubbles":["hmm"]}`, "reply", "hmm"},
		{"wah really ah\n\nhow to join?", "reply", "wah really ah\n\nhow to join?"}, // old plain-text format
		{`{"action":"reply","bubbles":["unterminated`, "reply", ""},              // broken JSON is never sent
	}
	for _, c := range cases {
		a, txt := ParseReply(c.in)
		if a != c.action || txt != c.text {
			t.Errorf("ParseReply(%q) = %q, %q; want %q, %q", c.in, a, txt, c.action, c.text)
		}
	}
}

var reDigits = regexp.MustCompile(`\d+`)

func TestAddTyposNeverTouchesDigitsAndPassesGuard(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	in := []string{"ok i transfer rm500 to the maybank one ending 1893", "wallet TXyz9876abcd ah? network what", "can call me at 5pm later, after 12 ok"}
	typos, fixes := 0, 0
	for i := 0; i < 2000; i++ {
		out := AddTypos(in, 1, r)
		if len(out) > maxBubbles {
			t.Fatalf("too many bubbles: %q", out)
		}
		var typed []string
		for _, b := range out {
			if strings.HasPrefix(b, "*") {
				fixes++
				continue
			}
			typed = append(typed, b)
			if g, ok := Guard("Auntie Mei", b); !ok || len(g) != 1 {
				t.Fatalf("typo bubble fails the guard: %q", b)
			}
		}
		for j, b := range typed {
			if strings.Join(reDigits.FindAllString(b, -1), ",") != strings.Join(reDigits.FindAllString(in[j], -1), ",") {
				t.Fatalf("digits changed: %q -> %q", in[j], b)
			}
			if strings.Contains(b, "TXyz9876abcd") != strings.Contains(in[j], "TXyz9876abcd") || len(b) != len(in[j]) {
				t.Fatalf("typo touched a code-like token or changed length: %q -> %q", in[j], b)
			}
			if b != in[j] {
				typos++
			}
		}
	}
	if typos == 0 || fixes == 0 {
		t.Fatalf("typos %d, fixes %d", typos, fixes)
	}
	if out := AddTypos(in, 0.0001, rand.New(rand.NewSource(1))); strings.Join(out, "|") != strings.Join(in, "|") {
		t.Errorf("rate ~0 changed the reply: %q", out)
	}
}

func TestSystemPromptAsksForJSONAndTimingNotes(t *testing.T) {
	base := Situation{Stage: playbook.Generic(playbook.Rapport), Now: time.Now()}
	got := SystemPrompt(Profile{Name: "Mei", Age: 60}, base)
	for _, want := range []string{`{"action":"reply","bubbles":["..."]}`, `"ignore"`, `"brb"`, "Match their length", "do not have to answer every question"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	for _, bad := range []string{"were away", "only seeing their message now", "left their last message on read"} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected note %q", bad)
		}
	}
	base.WasAway, base.NoIgnore = true, true
	got = SystemPrompt(Profile{Name: "Mei"}, base)
	if !strings.Contains(got, "sorry just now driving") || !strings.Contains(got, "Answer this time") {
		t.Errorf("away/no-ignore notes missing:\n%s", got)
	}
	base.WasAway, base.LateBy = false, 40*time.Minute
	if got = SystemPrompt(Profile{Name: "Mei"}, base); !strings.Contains(got, "Open casually") {
		t.Errorf("long-gap note missing")
	}
}
