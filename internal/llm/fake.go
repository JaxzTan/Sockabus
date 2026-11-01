package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
)

// Fake is the offline provider for CI and local tests (SOCKABUS_LLM_PROVIDER=fake, TRD §9). It
// never calls a network API, and the same request always gets the same answer. It recognises
// the three structured prompts by their wording, so it must change if those prompts change:
//   - extraction ("extract scam intelligence") → no extra entities; the regex extractor still runs
//   - classification ("Classify a scam conversation") → "other", so the keyword classifier decides
//   - persona generator ("## Endpoint mode") → valid personas, named from a hash of the input
//
// Everything else is a persona reply, picked from a fixed list by the scammer's latest message.
type Fake struct{}

func NewFake() Fake { return Fake{} }

func (Fake) Name() string { return "fake" }

func (Fake) Generate(_ context.Context, req Request) (string, error) {
	var last string
	for i := len(req.Turns) - 1; i >= 0; i-- {
		if req.Turns[i].Role == RoleUser {
			last = req.Turns[i].Text
			break
		}
	}
	switch {
	case strings.Contains(req.System, "extract scam intelligence"):
		return `{"entities":[]}`, nil
	case strings.Contains(req.System, "Classify a scam conversation"):
		return `{"label":"other"}`, nil
	case strings.Contains(req.System, "## Endpoint mode"):
		return fakePersonas(last)
	}
	return fakeReplies[hash(last)%uint32(len(fakeReplies))], nil
}

// No digits: the persona output guard rejects long digit runs.
var fakeReplies = []string{
	"ok can.. how i pay ah?",
	"wah really? which account i send to",
	"sorry just saw ur msg\n\nyou can send the bank details again?",
	"hmm my son say must be careful.. got proof or not",
	"i not so good with phone, explain slowly ya",
}

var (
	fakeGiven  = []string{"Mei Ling", "Siew Lan", "Rosnah", "Kamala", "Ah Hock", "Rahim", "Lily", "Bee Choo"}
	fakeFamily = []string{"Tan", "Lim", "Wong", "Ismail", "Raj", "Ong", "Yusof", "Chua"}
	fakeCities = []string{"Penang", "Ipoh", "Kuching", "Johor Bahru", "Melaka", "Kota Bharu"}
)

// fakePersonas answers the generator's JSON input ({"count", "existing_personas", ...}) with
// that many personas whose names are not already taken.
func fakePersonas(input string) (string, error) {
	var in struct {
		Count    int `json:"count"`
		Existing []struct {
			Name string `json:"name"`
		} `json:"existing_personas"`
	}
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", fmt.Errorf("fake llm: generator input is not JSON: %w", err)
	}
	taken := map[string]bool{}
	for _, e := range in.Existing {
		taken[strings.ToLower(e.Name)] = true
	}
	h := hash(input)
	out := make([]map[string]any, 0, max(in.Count, 1))
	for i := uint32(0); len(out) < max(in.Count, 1); i++ {
		n := h + i*7919
		name := fmt.Sprintf("%s %s %s", fakeGiven[n%8], fakeFamily[(n/8)%8], letters(n))
		if taken[strings.ToLower(name)] {
			continue
		}
		taken[strings.ToLower(name)] = true
		age := 25 + int(n%55)
		city := fakeCities[n%uint32(len(fakeCities))]
		out = append(out, map[string]any{
			"persona": map[string]any{
				"name":           name,
				"age":            age,
				"language_mix":   map[string]float64{"en": 0.6, "ms": 0.4},
				"backstory":      fmt.Sprintf("Test persona from %s. Lives alone, chats on the phone most evenings.", city),
				"wealth_profile": map[string]any{"savings_band": "RM50k-100k", "tech_savvy": "low", "banks": []string{"Maybank"}},
				"status":         "draft",
			},
			"profile":          map[string]any{"gender": "female", "city": city, "state": city, "occupation": "retired"},
			"demo_identifiers": nil,
			"prompt_version": map[string]any{
				"version":       1,
				"system_prompt": fmt.Sprintf("You are %s, %d, from %s. Reply in short Manglish chat messages.", name, age, city),
			},
			"generation_notes": nil,
		})
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// letters turns n into a short all-letter tag, so generated names never contain digits.
func letters(n uint32) string {
	b := make([]byte, 4)
	for i := range b {
		b[i] = byte('a' + n%26)
		n /= 26
	}
	return strings.ToUpper(string(b[:1])) + string(b[1:])
}

func hash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}
