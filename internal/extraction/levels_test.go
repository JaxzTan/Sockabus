package extraction

import (
	"context"
	"errors"
	"testing"

	"github.com/JaxzTan/Buru_AI/internal/llm"
)

func TestLevel(t *testing.T) {
	cases := []struct {
		score float64
		want  string
	}{
		{0, "low"}, {0.6, "low"}, {0.79, "low"}, {0.8, "medium"}, {0.92, "medium"}, {1, "medium"},
	}
	for _, c := range cases {
		if got := Level(c.score); got != c.want {
			t.Errorf("Level(%v) = %s, want %s", c.score, got, c.want)
		}
	}
}

func TestClassifyLoan(t *testing.T) {
	if got := Classify("Pinjaman peribadi low interest, pay processing fee RM300 first"); got != "loan" {
		t.Errorf("Classify = %s, want loan", got)
	}
}

func TestNormalizeValue(t *testing.T) {
	cases := []struct {
		typ, in, want string
		ok            bool
	}{
		{"bank_account", "5621 0384 1192", "562103841192", true},
		{"bank_account", "12345", "", false},
		{"phone", "011-2345 6789", "+601123456789", true},
		{"phone", "+60 3-1234 5678", "", false}, // landline is not a mobile
		{"wallet", "0xAbC0000000000000000000000000000000000001", "0xabc0000000000000000000000000000000000001", true},
		{"wallet", "TUiNnd7ic6yCmaLmyuE7g7ZF5XYURYLBwR", "TUiNnd7ic6yCmaLmyuE7g7ZF5XYURYLBwR", true},
		{"wallet", "TQ7vR2mXc8kLpW4nYb6sHd3fJ9eA1u9xKp", "", false},
		{"url", "HTTP://Lazada-Task.top/verify/", "lazada-task.top/verify", true},
		{"handle", "@Gold_USDT_Mentor", "@gold_usdt_mentor", true},
		{"handle", "tg:123456", "tg:123456", true},
		{"handle", "@x", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeValue(c.typ, c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("NormalizeValue(%s, %q) = %q, %v; want %q, %v", c.typ, c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestBankOrChain(t *testing.T) {
	if BankOrChain("wallet", "TRON") != "TRON" || BankOrChain("wallet", "eth") != "ETHEREUM" || BankOrChain("bank_account", "mbb") != "MBB" ||
		BankOrChain("handle", "telegram") != "TELEGRAM" || BankOrChain("url", "") != "" {
		t.Error("BankOrChain mapping")
	}
}

type fakeLLM struct {
	out string
	err error
}

func (f fakeLLM) Generate(context.Context, llm.Request) (string, error) { return f.out, f.err }
func (f fakeLLM) Name() string                                          { return "fake" }

func TestExtractWithLLMGrounding(t *testing.T) {
	text := "akaun maybank saya satu enam empat dua... nanti hantar. Contact @boss_kl_99 for slot"
	// The model proposes one grounded handle, one invented wallet, and one invented account.
	out := `{"entities":[{"type":"handle","value":"@boss_kl_99"},{"type":"wallet","value":"TUiNnd7ic6yCmaLmyuE7g7ZF5XYURYLBwR"},{"type":"bank_account","value":"164204571893","bank":"Maybank"}]}`
	got, err := ExtractWithLLM(context.Background(), fakeLLM{out: out}, text)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != "handle" || got[0].Value != "@boss_kl_99" || Level(got[0].Confidence) != "low" {
		t.Fatalf("got %+v, want only the grounded handle at low confidence", got)
	}
}

func TestExtractWithLLMFallsBack(t *testing.T) {
	got, err := ExtractWithLLM(context.Background(), fakeLLM{err: errors.New("down")}, "send to Maybank 5621 0384 1192")
	if err == nil || len(got) != 1 || got[0].Type != "bank_account" {
		t.Fatalf("want regex result plus error, got %+v %v", got, err)
	}
}

func TestClassifyWithLLM(t *testing.T) {
	if got := ClassifyWithLLM(context.Background(), fakeLLM{out: `{"label":"loan"}`}, "need money?"); got != "loan" {
		t.Errorf("got %s", got)
	}
	if got := ClassifyWithLLM(context.Background(), fakeLLM{out: `{"label":"hacking"}`}, "part time job RM300 per day"); got != "job" {
		t.Errorf("unknown label must fall back to keywords, got %s", got)
	}
}
