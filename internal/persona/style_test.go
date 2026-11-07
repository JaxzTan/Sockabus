package persona

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JaxzTan/Buru_AI/internal/playbook"
)

func TestGroupFor(t *testing.T) {
	cases := []struct {
		eth, mix string
		want     Group
	}{
		{"Chinese", ``, GroupChinese},
		{"Malay", ``, GroupMalay},
		{"Indian", ``, GroupIndian},
		{"Indian (Tamil)", ``, GroupIndian},
		{"Iban", `{"ms":0.9}`, GroupNone}, // known ethnicity without a pack beats the mix
		{"", `{"nan":0.5,"en":0.5}`, GroupChinese},
		{"", `{"en":0.55,"ta":0.3,"ms":0.15}`, GroupIndian},
		{"", `{"ms":0.75,"en":0.25}`, GroupMalay},
		{"", `{"en":1}`, GroupNone},
		{"", ``, GroupNone},
	}
	for _, c := range cases {
		if got := GroupFor(c.eth, json.RawMessage(c.mix)); got != c.want {
			t.Errorf("GroupFor(%q, %s) = %q, want %q", c.eth, c.mix, got, c.want)
		}
	}
}

func TestTypingStyleByAge(t *testing.T) {
	if TypingStyle(GroupNone, 40) != "" {
		t.Fatal("GroupNone must give no section")
	}
	old, young, mid := TypingStyle(GroupMalay, 60), TypingStyle(GroupMalay, 22), TypingStyle(GroupMalay, 45)
	if !strings.Contains(old, "Assalamualaikum") || !strings.Contains(young, "kahkah") {
		t.Fatalf("age lines missing:\nold=%s\nyoung=%s", old, young)
	}
	if strings.Contains(mid, "At your age") {
		t.Fatalf("mid-age persona should get no age line: %s", mid)
	}
	for _, g := range Groups {
		if s := TypingStyle(g, 30); !strings.HasPrefix(s, "## How you type") || !strings.Contains(s, "Do not overdo it") {
			t.Errorf("%s pack malformed: %s", g, s)
		}
	}
}

func TestSystemPromptIncludesStyle(t *testing.T) {
	sit := Situation{Stage: playbook.Generic(playbook.Rapport), Now: time.Now()}
	// Composed prompt and stored prompt both get the pack.
	for _, p := range []Profile{
		{Name: "Arvin", Age: 29, Ethnicity: "Indian", LanguageMix: json.RawMessage(`{"en":0.7,"ta":0.3}`)},
		{Name: "Arvin", Age: 29, Ethnicity: "Indian", SystemPrompt: "You are Arvin."},
	} {
		if s := SystemPrompt(p, sit); !strings.Contains(s, "Indian Malaysian people text") {
			t.Errorf("style pack missing from prompt (stored=%v)", p.SystemPrompt != "")
		}
	}
	if s := SystemPrompt(Profile{Name: "X", Age: 30}, sit); strings.Contains(s, "How you type") {
		t.Error("persona without ethnicity or mix must not get a pack")
	}
}

func TestSystemPromptSimilar(t *testing.T) {
	sit := Situation{Stage: playbook.Generic(playbook.Bait), Now: time.Now()}
	if strings.Contains(SystemPrompt(Profile{Name: "A", Age: 40}, sit), "What worked before") {
		t.Fatal("no examples → no section")
	}
	sit.Similar = []string{"aiyo app error, got other account?"}
	s := SystemPrompt(Profile{Name: "A", Age: 40}, sit)
	if !strings.Contains(s, "## What worked before") || !strings.Contains(s, `"aiyo app error, got other account?"`) {
		t.Fatalf("examples missing:\n%s", s)
	}
	if strings.Index(s, "What worked before") > strings.Index(s, "Rules you always follow") {
		t.Error("examples must come before the rules so the rules have the last word")
	}
}

func TestTypingStyleChineseFirst(t *testing.T) {
	zh := json.RawMessage(`{"zh":0.65,"en":0.25,"ms":0.1}`)
	manglish := json.RawMessage(`{"en":0.6,"yue":0.25,"ms":0.15}`)
	if !strings.Contains(TypingStyleMix(GroupChinese, 38, zh), "Chinese is your main language") {
		t.Error("Mandarin-first persona must get the Chinese-script line")
	}
	if strings.Contains(TypingStyleMix(GroupChinese, 26, manglish), "Chinese is your main language") {
		t.Error("English-first Chinese persona must not")
	}
	if strings.Contains(TypingStyleMix(GroupMalay, 40, zh), "Chinese is your main language") {
		t.Error("only the Chinese pack gets it")
	}
}
