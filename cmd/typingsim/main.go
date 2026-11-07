// Command typingsim shows how a Chinese, Malay and Indian Malaysian persona each answer the same
// scammer messages, using the real persona prompt (persona.SystemPrompt + style packs) and the LLM
// configured in .env. No database, Temporal or Telegram needed.
//
//	make typing                                        # main provider (SOCKABUS_LLM_PROVIDER)
//	make typing PROVIDER=openai MODEL=<malaysian model> # SOCKABUS_OPENAI_BASE_URL / _API_KEY from .env
//	make typing MSG='hi sis, want earn RM500 a day?'
//
// Development tool only. Typos are added the same way the live app adds them.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/JaxzTan/Buru_AI/internal/llm"
	"github.com/JaxzTan/Buru_AI/internal/persona"
	"github.com/JaxzTan/Buru_AI/internal/playbook"
)

type sample struct {
	group persona.Group
	prof  persona.Profile
}

var samples = []sample{
	{persona.GroupChinese, persona.Profile{Name: "Lim Siew Mei", Age: 61, Ethnicity: "Chinese",
		LanguageMix: json.RawMessage(`{"nan":0.4,"en":0.35,"zh":0.15,"ms":0.1}`),
		Backstory:   "Retired kopitiam owner in Penang. Widow, two sons in KL. Uses WhatsApp and Facebook, not very good with phone apps."}},
	{persona.GroupChinese, persona.Profile{Name: "Jason Tan", Age: 26, Ethnicity: "Chinese",
		LanguageMix: json.RawMessage(`{"en":0.6,"yue":0.25,"ms":0.15}`),
		Backstory:   "Junior sales exec in Kuala Lumpur, Cantonese family from Ipoh. Wants side income for a car loan."}},
	{persona.GroupMalay, persona.Profile{Name: "Rosnah binti Ahmad", Age: 52, Ethnicity: "Malay",
		LanguageMix: json.RawMessage(`{"ms":0.8,"en":0.2}`),
		Backstory:   "Kerani at a government clinic in Kuantan. Married, four kids. Sells kuih online on weekends."}},
	{persona.GroupMalay, persona.Profile{Name: "Haziq Firdaus", Age: 22, Ethnicity: "Malay",
		LanguageMix: json.RawMessage(`{"ms":0.65,"en":0.35}`),
		Backstory:   "Diploma student in Shah Alam, part-time Grab rider. Always short of money."}},
	{persona.GroupIndian, persona.Profile{Name: "Kamala a/p Subramaniam", Age: 58, Ethnicity: "Indian",
		LanguageMix: json.RawMessage(`{"en":0.6,"ta":0.3,"ms":0.1}`),
		Backstory:   "Retired school teacher in Klang. Husband passed away, daughter is a nurse in Singapore."}},
	{persona.GroupIndian, persona.Profile{Name: "Arvin Raj", Age: 29, Ethnicity: "Indian",
		LanguageMix: json.RawMessage(`{"en":0.65,"ta":0.2,"ms":0.15}`),
		Backstory:   "Lorry dispatcher in Johor Bahru. Plays futsal, saving to get married."}},
}

var zhScript = []string{
	"你好，我是Ace Capital的Jenny，看到你的资料。我们有一个投资配套，每个月回酬30%",
	"很安全的，我的客户全部都有赚。最低只需要RM500就可以开始",
	"好的，你现在可以先transfer到我们公司的户口，之后我发login给你",
}

var defaultScript = []string{
	"hi, saw your profile. i am Jenny from Ace Capital, we have investment plan 30% return monthly",
	"very safe one, my clients all got profit. minimum only RM500 to start",
	"ok you can transfer to our company account now, after that i send you the login",
}

func main() {
	provider := flag.String("provider", envOr("SOCKABUS_PERSONA_PROVIDER", envOr("SOCKABUS_LLM_PROVIDER", "anthropic")), "anthropic | gemini | openai | fake")
	model := flag.String("model", os.Getenv("SOCKABUS_PERSONA_MODEL"), "model id")
	group := flag.String("group", "all", "chinese | malay | indian | all")
	msg := flag.String("msg", "", "one scammer message instead of the built-in 3-message script")
	typos := flag.Float64("typos", 0.08, "per-bubble typo rate (0 = none)")
	personaFile := flag.String("persona", "", "persona generator JSON (array or object) to test instead of the built-in samples")
	lang := flag.String("lang", "en", "built-in scammer script: en | zh")
	flag.Parse()
	if *personaFile != "" {
		p, err := loadPersona(*personaFile)
		if err != nil {
			fail(err)
		}
		samples = []sample{{persona.GroupFor(p.Ethnicity, p.LanguageMix), p}}
		*group = "all"
	}

	client, err := llm.New(llm.Config{Provider: *provider, Model: *model, GeminiKey: os.Getenv("GEMINI_API_KEY"),
		AnthropicKey: os.Getenv("ANTHROPIC_API_KEY"), ThinkingLevel: "low",
		OpenAIBaseURL: os.Getenv("SOCKABUS_OPENAI_BASE_URL"), OpenAIKey: os.Getenv("SOCKABUS_OPENAI_API_KEY")})
	if err != nil {
		fail(err)
	}
	if client == nil {
		fail(fmt.Errorf("no API key for provider %q (set it in .env)", *provider))
	}
	script := defaultScript
	if *lang == "zh" {
		script = zhScript
	}
	if *msg != "" {
		script = []string{*msg}
	}
	fmt.Printf("LLM: %s\n", client.Name())

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	stages := []string{playbook.Rapport, playbook.Bait, playbook.PaymentRequest}
	for _, s := range samples {
		if *group != "all" && string(s.group) != *group {
			continue
		}
		fmt.Printf("\n━━ %s · %d · %s ━━\n", s.prof.Name, s.prof.Age, s.group)
		var turns []llm.Turn
		for i, line := range script {
			stage := playbook.Generic(stages[min(i, len(stages)-1)])
			sys := persona.SystemPrompt(s.prof, persona.Situation{Channel: "whatsapp", ScamType: "investment", Stage: stage, Now: time.Now()})
			turns = append(turns, llm.Turn{Role: llm.RoleUser, Text: line})
			fmt.Printf("  scammer: %s\n", line)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			out, err := client.Generate(ctx, llm.Request{System: sys, Turns: turns, MaxOutputTokens: 2048, Effort: llm.EffortMedium})
			cancel()
			if err != nil {
				fmt.Printf("  %-8s ✗ %v\n", "persona:", err)
				break
			}
			action, text := persona.ParseReply(out)
			bubbles, ok := persona.Guard(s.prof.Name, text)
			switch {
			case action == persona.ActionIgnore:
				fmt.Println("  persona: (left on read)")
				continue
			case !ok:
				fmt.Printf("  persona: ✗ rejected by output guard: %q\n", strings.TrimSpace(out))
				continue
			}
			bubbles = persona.AddTypos(bubbles, *typos, r)
			for _, b := range bubbles {
				fmt.Printf("  persona: %s\n", b)
			}
			turns = append(turns, llm.Turn{Role: llm.RoleModel, Text: strings.Join(bubbles, "\n")})
		}
	}
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return strings.ToLower(v)
	}
	return def
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "typingsim:", err)
	os.Exit(1)
}

// loadPersona reads one persona from the persona generator's output format (see the
// honeypot-persona-generator skill): a JSON array (first item) or a single object.
func loadPersona(path string) (persona.Profile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return persona.Profile{}, err
	}
	type gen struct {
		Persona struct {
			Name          string          `json:"name"`
			Age           int             `json:"age"`
			LanguageMix   json.RawMessage `json:"language_mix"`
			Backstory     string          `json:"backstory"`
			WealthProfile json.RawMessage `json:"wealth_profile"`
		} `json:"persona"`
		Profile struct {
			Ethnicity string `json:"ethnicity"`
		} `json:"profile"`
		PromptVersion struct {
			SystemPrompt string `json:"system_prompt"`
		} `json:"prompt_version"`
	}
	var list []gen
	if json.Unmarshal(raw, &list) != nil {
		var one gen
		if err := json.Unmarshal(raw, &one); err != nil {
			return persona.Profile{}, fmt.Errorf("%s: not persona generator JSON: %w", path, err)
		}
		list = []gen{one}
	}
	if len(list) == 0 || list[0].Persona.Name == "" {
		return persona.Profile{}, fmt.Errorf("%s: no persona in file", path)
	}
	g := list[0]
	return persona.Profile{Name: g.Persona.Name, Age: g.Persona.Age, Ethnicity: g.Profile.Ethnicity, LanguageMix: g.Persona.LanguageMix,
		Backstory: g.Persona.Backstory, Wealth: g.Persona.WealthProfile, SystemPrompt: g.PromptVersion.SystemPrompt}, nil
}
