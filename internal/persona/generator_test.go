package persona

import (
	"context"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/JaxzTan/Buru_AI/internal/llm"
)

type stubLLM struct {
	out string
	got llm.Request
}

func (s *stubLLM) Generate(_ context.Context, r llm.Request) (string, error) {
	s.got = r
	return s.out, nil
}
func (s *stubLLM) Name() string { return "stub" }

const sampleOutput = `[
 {"persona":{"name":"Kavitha a/p Subramaniam","age":30,"language_mix":{"en":0.55,"ta":0.3,"ms":0.15},"backstory":"Staff nurse in Klang.","wealth_profile":{"banks":["Maybank","BSN"]},"status":"active"},
  "profile":{"gender":"female","ethnicity":"Indian","city":"Klang","state":"Selangor","occupation":"Staff nurse"},
  "demo_identifiers":{"mykad":"960312-10-5678"},
  "prompt_version":{"version":3,"system_prompt":"You are Kavitha a/p Subramaniam, a 30-year-old Indian female living in Klang..."},
  "generation_notes":null},
 {"persona":{"name":"Tan Ah Kow","age":66,"language_mix":{"nan":2,"en":2},"backstory":"Retired.","wealth_profile":null},
  "profile":{"gender":"male"},
  "prompt_version":{"system_prompt":"You are Tan Ah Kow. Your IC is 600101-07-1235."}},
 {"persona":{"name":"Auntie Mei","age":62,"language_mix":{"en":1},"backstory":"x"},"prompt_version":{"system_prompt":"You are Auntie Mei"}},
 {"persona":{"name":"Nurul Aisyah binti Rahman","age":41,"language_mix":{"ms":3,"en":1},"backstory":"Civil servant."},
  "profile":{"gender":"female","city":"Putrajaya"},
  "prompt_version":{"system_prompt":"You are Nurul Aisyah binti Rahman..."}}
]`

func TestGenerateValidatesAndMasks(t *testing.T) {
	stub := &stubLLM{out: sampleOutput}
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	got, notes, err := Generate(context.Background(), stub, GenerateRequest{Count: 5, Mode: "weird"},
		[]Existing{{Name: "Auntie Mei", Age: 62}}, now, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Persona.Name != "Kavitha a/p Subramaniam" || got[1].Persona.Name != "Nurul Aisyah binti Rahman" {
		t.Fatalf("got %d personas: %+v (notes %q)", len(got), got, notes)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "identifier-like") || !strings.Contains(notes[1], "already used") {
		t.Errorf("notes = %q", notes)
	}
	for _, g := range got {
		if g.Persona.Status != "draft" || g.PromptVersion.Version != 1 {
			t.Errorf("%s: status %s version %d, want draft v1", g.Persona.Name, g.Persona.Status, g.PromptVersion.Version)
		}
		var ids struct {
			DemoOnly    bool   `json:"demo_only"`
			MyKad       string `json:"mykad"`
			Phone       string `json:"phone"`
			BankAccount struct {
				Number string `json:"number"`
			} `json:"bank_account"`
		}
		if err := json.Unmarshal(g.DemoIdentifiers, &ids); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(ids.MyKad, "-")
		if !ids.DemoOnly || len(parts) != 3 || parts[1] != "00" || !strings.Contains(ids.Phone, "•") || !strings.HasPrefix(ids.BankAccount.Number, "••••") {
			t.Errorf("%s: demo identifiers not safely masked: %+v", g.Persona.Name, ids)
		}
		if g.Persona.Name == "Kavitha a/p Subramaniam" && ids.MyKad[:2] != "96" {
			t.Errorf("MyKad year %s does not match age 30", ids.MyKad[:2])
		}
		if last := ids.MyKad[len(ids.MyKad)-1] - '0'; last%2 != 0 {
			t.Errorf("%s is female: last MyKad digit must be even, got %d", g.Persona.Name, last)
		}
	}
	if sum := got[1].Persona.LanguageMix["ms"] + got[1].Persona.LanguageMix["en"]; sum < 0.99 || sum > 1.01 {
		t.Errorf("language mix not normalised: %v", got[1].Persona.LanguageMix)
	}
	if strings.HasPrefix(stub.got.System, "---") || !strings.Contains(stub.got.System, "Endpoint mode") || !strings.Contains(stub.got.System, "Honeypot Persona Generator") {
		t.Error("system prompt should be the spec without frontmatter plus endpoint mode")
	}
	var input map[string]any
	_ = json.Unmarshal([]byte(stub.got.Turns[0].Text), &input)
	if input["mode"] != "random" || input["today"] != "2026-10-23" {
		t.Errorf("generator input = %v", input)
	}
}

func TestGenerateCapsBatch(t *testing.T) {
	stub := &stubLLM{out: sampleOutput}
	_, notes, _ := Generate(context.Background(), stub, GenerateRequest{Count: 25}, nil, time.Now(), rand.New(rand.NewSource(1)))
	if len(notes) == 0 || !strings.Contains(notes[0], "capped at 10") {
		t.Errorf("notes = %q", notes)
	}
}

func TestGenerateNoClient(t *testing.T) {
	if _, _, err := Generate(context.Background(), nil, GenerateRequest{}, nil, time.Now(), rand.New(rand.NewSource(1))); err == nil {
		t.Fatal("expected error without an LLM")
	}
}

func TestGenerateWithFakeLLM(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	first, _, err := Generate(context.Background(), llm.NewFake(), GenerateRequest{Count: 2}, nil, now, rand.New(rand.NewSource(1)))
	if err != nil || len(first) != 2 {
		t.Fatalf("Generate = %d personas, %v", len(first), err)
	}
	// The next click sees the first personas as existing, so it must give new names.
	existing := []Existing{{Name: first[0].Persona.Name}, {Name: first[1].Persona.Name}}
	next, _, err := Generate(context.Background(), llm.NewFake(), GenerateRequest{Count: 1}, existing, now, rand.New(rand.NewSource(1)))
	if err != nil || len(next) != 1 {
		t.Fatalf("second Generate = %d personas, %v", len(next), err)
	}
	if n := next[0].Persona.Name; n == first[0].Persona.Name || n == first[1].Persona.Name {
		t.Fatalf("second click reused name %q", n)
	}
}
