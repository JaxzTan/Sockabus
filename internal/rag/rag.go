// Package rag retrieves how our side (persona or operator) answered similar scammer messages in
// past conversations that ended with a bank account or wallet extracted, so the persona can reuse
// what worked.
//
// Design constraints:
//   - Message bodies are encrypted at rest (TR-14), so the index lives in memory only, is built from
//     decrypted rows, and is rebuilt periodically (which also drops rows the retention purge removed).
//     No embeddings or plaintext-derived data are written anywhere.
//   - Scammer text is used for ranking only. Only our own past replies are returned, sanitised of
//     digits, links and handles, because the result goes into the system prompt (TRD §7).
package rag

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Pair is one past exchange: what the scammer said and how our side answered it.
type Pair struct {
	ConversationID int64
	PersonaID      int64
	Group          string // persona.Group of the persona that answered ("" unknown)
	ScamType       string
	Scammer        string // ranking only; never returned in a prompt
	Reply          string // already sanitised
}

// Query is the current situation to find examples for.
type Query struct {
	Text           string // the scammer's latest message(s)
	ScamType       string
	Group          string
	PersonaID      int64
	ConversationID int64 // excluded: never retrieve from the chat being answered
}

// Index is a BM25 index over Pair.Scammer. It is safe for concurrent Search and Replace.
type Index struct {
	mu    sync.RWMutex
	pairs []Pair
	terms []map[string]int // per pair term frequency
	lens  []int
	df    map[string]int
	avgdl float64
}

// Replace swaps the whole index for pairs.
func (ix *Index) Replace(pairs []Pair) {
	terms := make([]map[string]int, len(pairs))
	lens := make([]int, len(pairs))
	df := map[string]int{}
	total := 0
	for i, p := range pairs {
		tf := map[string]int{}
		for _, t := range Tokens(p.Scammer) {
			tf[t]++
			lens[i]++
		}
		for t := range tf {
			df[t]++
		}
		terms[i] = tf
		total += lens[i]
	}
	avg := 0.0
	if len(pairs) > 0 {
		avg = float64(total) / float64(len(pairs))
	}
	ix.mu.Lock()
	ix.pairs, ix.terms, ix.lens, ix.df, ix.avgdl = pairs, terms, lens, df, avg
	ix.mu.Unlock()
}

// Len is the number of indexed pairs.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.pairs)
}

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// Boosts on top of text relevance.
	boostScamType = 1.3
	boostGroup    = 1.25
	boostPersona  = 1.15
	// A result needs at least this BM25 score (before boosts) to count as similar.
	minScore = 1.0
)

// Search returns up to k replies, best first, at most one per conversation and no duplicates.
func (ix *Index) Search(q Query, k int) []Pair {
	qt := uniq(Tokens(q.Text))
	if k <= 0 || len(qt) == 0 {
		return nil
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	n := float64(len(ix.pairs))
	type hit struct {
		i     int
		score float64
	}
	var hits []hit
	for i, p := range ix.pairs {
		if p.ConversationID == q.ConversationID {
			continue
		}
		s := 0.0
		for _, t := range qt {
			f := float64(ix.terms[i][t])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(ix.df[t])+0.5)/(float64(ix.df[t])+0.5))
			s += idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(ix.lens[i])/ix.avgdl))
		}
		if s < minScore {
			continue
		}
		if q.ScamType != "" && p.ScamType == q.ScamType {
			s *= boostScamType
		}
		if q.Group != "" && p.Group == q.Group {
			s *= boostGroup
		}
		if q.PersonaID != 0 && p.PersonaID == q.PersonaID {
			s *= boostPersona
		}
		hits = append(hits, hit{i, s})
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	var out []Pair
	seenConv := map[int64]bool{}
	seenReply := map[string]bool{}
	for _, h := range hits {
		p := ix.pairs[h.i]
		key := strings.ToLower(p.Reply)
		if seenConv[p.ConversationID] || seenReply[key] {
			continue
		}
		seenConv[p.ConversationID], seenReply[key] = true, true
		out = append(out, p)
		if len(out) == k {
			break
		}
	}
	return out
}

// Message is one decrypted chat message, as Build needs it.
type Message struct {
	ConversationID int64
	PersonaID      int64
	Group          string
	ScamType       string
	Inbound        bool // from the scammer
	Text           string
}

// Build turns messages (grouped by conversation, in chat order) into pairs: each outbound message
// is paired with the inbound messages since our previous outbound one. Consecutive outbound
// bubbles are joined into one reply.
func Build(msgs []Message) []Pair {
	var out []Pair
	var scammer []string
	var cur *Pair
	flush := func() {
		if cur != nil {
			if cur.Reply = Sanitize(cur.Reply); cur.Reply != "" && strings.TrimSpace(cur.Scammer) != "" {
				out = append(out, *cur)
			}
			cur = nil
		}
	}
	var conv int64 = -1
	for _, m := range msgs {
		if m.ConversationID != conv {
			flush()
			scammer, conv = nil, m.ConversationID
		}
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		if m.Inbound {
			flush()
			scammer = append(scammer, text)
			continue
		}
		if cur == nil {
			if len(scammer) == 0 {
				continue // we spoke first; nothing to match against
			}
			cur = &Pair{ConversationID: m.ConversationID, PersonaID: m.PersonaID, Group: m.Group, ScamType: m.ScamType,
				Scammer: strings.Join(scammer, "\n")}
			scammer = nil
		}
		if cur.Reply != "" {
			cur.Reply += " / "
		}
		cur.Reply += text
	}
	flush()
	return out
}

var (
	reURL      = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+|\b\S+\.(?:com|my|net|org|io|xyz|link|cc|me)(?:/\S*)?\b`)
	reHandle   = regexp.MustCompile(`@\w+`)
	reDigits   = regexp.MustCompile(`\d[\d\s-]{2,}\d|\d{3,}`)
	reEndsWith = regexp.MustCompile(`(?i)\bend(?:s|ing)?\s+(?:with\s+)?\S*\d\S*`)
	reSpaces   = regexp.MustCompile(`\s+`)
)

// MaxReply caps a retrieved reply's length in runes.
const MaxReply = 220

// Sanitize removes things that belong to another conversation (links, handles, numbers, "ending
// 1893") and caps the length. It returns "" when too little is left to be useful.
func Sanitize(s string) string {
	s = reURL.ReplaceAllString(s, "[link]")
	s = reHandle.ReplaceAllString(s, "[handle]")
	s = reEndsWith.ReplaceAllString(s, "ending …")
	s = reDigits.ReplaceAllString(s, "…")
	s = strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
	if r := []rune(s); len(r) > MaxReply {
		s = strings.TrimSpace(string(r[:MaxReply])) + "…"
	}
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < 4 {
		return ""
	}
	return s
}

// stopwords are frequent English/Malay/Manglish words that carry no meaning for matching.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`the a an and or to of in on for is are was be it this that i you u me my your we our they
		he she so do can will just ok la lah ah eh one got what how not no yes ya ye already very also
		dan yang ini itu ke di nak nk tak x sy saya awak dah dh je pun ada kan ni tu dgn utk boleh blh`) {
		stopwords[w] = true
	}
}

// Tokens lowercases s and splits it into words of 2+ letters/digits, without stopwords.
func Tokens(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(f)) < 2 || stopwords[f] {
			continue
		}
		out = append(out, f)
	}
	return out
}

func uniq(ss []string) []string {
	seen := map[string]bool{}
	out := ss[:0:0]
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
