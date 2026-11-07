package persona

import (
	"encoding/json"
	"math/rand"
	"strings"
)

// Reply actions the model can choose (see "How to reply" in SystemPrompt).
const (
	ActionReply  = "reply"
	ActionIgnore = "ignore"
	ActionBrb    = "brb"
)

// ParseReply reads the model's {"action":"reply|ignore|brb","bubbles":[...]} answer. It returns the
// action and the bubbles joined by blank lines, ready for Guard. Text that is not JSON is taken
// as a plain-text reply (the old format); text that looks like broken JSON returns "" so Guard
// rejects it rather than sending JSON to the scammer.
func ParseReply(raw string) (action, text string) {
	t := strings.TrimSpace(raw)
	if strings.HasPrefix(t, "```") { // ```json ... ```
		t = strings.TrimPrefix(t, "```")
		if nl := strings.IndexByte(t, '\n'); nl >= 0 {
			t = t[nl+1:]
		}
		t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "```"))
	}
	if i, j := strings.IndexByte(t, '{'), strings.LastIndexByte(t, '}'); i >= 0 && j > i {
		var v struct {
			Action  string   `json:"action"`
			Bubbles []string `json:"bubbles"`
		}
		if json.Unmarshal([]byte(t[i:j+1]), &v) == nil {
			action = strings.ToLower(strings.TrimSpace(v.Action))
			if action != ActionIgnore && action != ActionBrb {
				action = ActionReply
			}
			var bubbles []string
			for _, b := range v.Bubbles {
				if b = strings.TrimSpace(b); b != "" {
					bubbles = append(bubbles, b)
				}
			}
			return action, strings.Join(bubbles, "\n\n")
		}
	}
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		return ActionReply, ""
	}
	return ActionReply, raw
}

// Neighbouring keys on a QWERTY phone keyboard, letters only: a typo never touches or creates digits.
var qwertyNeighbours = map[byte]string{
	'q': "wa", 'w': "qeas", 'e': "wrsd", 'r': "etdf", 't': "ryfg", 'y': "tugh", 'u': "yihj", 'i': "uojk", 'o': "ipkl", 'p': "ol",
	'a': "qwsz", 's': "awedxz", 'd': "serfcx", 'f': "drtgvc", 'g': "ftyhbv", 'h': "gyujnb", 'j': "huikmn", 'k': "jiolm", 'l': "kop",
	'z': "asx", 'x': "zsdc", 'c': "xdfv", 'v': "cfgb", 'b': "vghn", 'n': "bhjm", 'm': "njk",
}

const maxBubbles = 4 // a reply plus its "*fix" bubbles

// AddTypos gives each bubble, with probability rate, one fat-finger typo: a letter swapped for an
// adjacent QWERTY key in a plain word of 3+ letters (never in tokens with digits, links or handles).
// Sometimes the persona corrects it in a follow-up "*word" bubble. Callers must run Guard on the
// result.
func AddTypos(bubbles []string, rate float64, r *rand.Rand) []string {
	out := make([]string, 0, len(bubbles)+1)
	added := 0
	for _, b := range bubbles {
		if r.Float64() >= rate {
			out = append(out, b)
			continue
		}
		typo, word, ok := fatFinger(b, r)
		if !ok {
			out = append(out, b)
			continue
		}
		out = append(out, typo)
		if len(bubbles)+added < maxBubbles && r.Float64() < 0.4 {
			out = append(out, "*"+word)
			added++
		}
	}
	return out
}

// fatFinger swaps one letter (never the first) of a random plain word in s.
func fatFinger(s string, r *rand.Rand) (typo, word string, ok bool) {
	type span struct{ start, end int }
	var words []span
	for i := 0; i < len(s); {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '\n' {
			i++
		}
		tok := s[start:i]
		if i < len(s) && s[i] == '\n' {
			i++
		}
		core := strings.TrimRight(tok, ".,!?;:)'\"")
		if len(core) >= 3 && isLetters(core) {
			words = append(words, span{start, start + len(core)})
		}
	}
	if len(words) == 0 {
		return s, "", false
	}
	w := words[r.Intn(len(words))]
	pos := w.start + 1 + r.Intn(w.end-w.start-1)
	c := s[pos]
	lower := c | 0x20
	n := qwertyNeighbours[lower]
	if n == "" {
		return s, "", false
	}
	repl := n[r.Intn(len(n))]
	if c != lower { // keep upper case
		repl &^= 0x20
	}
	return s[:pos] + string(repl) + s[pos+1:], s[w.start:w.end], true
}

func isLetters(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i] | 0x20; c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}
