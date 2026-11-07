package persona

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Group is a Malaysian community whose everyday texting habits the persona prompt describes, so a
// Chinese, Malay or Indian persona does not all sound like the same generic "Manglish" speaker.
type Group string

const (
	GroupNone    Group = ""
	GroupChinese Group = "chinese"
	GroupMalay   Group = "malay"
	GroupIndian  Group = "indian"
)

// Groups lists the groups that have a style pack, in display order.
var Groups = []Group{GroupChinese, GroupMalay, GroupIndian}

// GroupFor picks the style pack from the persona's profile ethnicity, or, when that is empty or
// unrecognised, from the dominant language family in its language_mix. Sabah/Sarawak natives and
// anything else unknown get GroupNone (no pack) rather than a wrong one.
func GroupFor(ethnicity string, mix json.RawMessage) Group {
	e := strings.ToLower(ethnicity)
	switch {
	case strings.Contains(e, "chinese") || strings.Contains(e, "cina"):
		return GroupChinese
	case strings.Contains(e, "indian") || strings.Contains(e, "india") || strings.Contains(e, "tamil") ||
		strings.Contains(e, "punjabi") || strings.Contains(e, "telugu") || strings.Contains(e, "malayalee"):
		return GroupIndian
	case strings.Contains(e, "malay") || strings.Contains(e, "melayu"):
		return GroupMalay
	case e != "":
		return GroupNone // e.g. Iban, Kadazan-Dusun: no pack yet
	}
	var m map[string]float64
	if json.Unmarshal(mix, &m) != nil {
		return GroupNone
	}
	chinese := m["zh"] + m["yue"] + m["nan"] + m["hak"]
	switch {
	case m["ta"] >= 0.15 && m["ta"] >= chinese:
		return GroupIndian
	case chinese >= 0.2:
		return GroupChinese
	case m["ms"] >= 0.5:
		return GroupMalay
	}
	return GroupNone
}

type stylePack struct {
	base, older, younger string
	samples              []string
}

var stylePacks = map[Group]stylePack{
	GroupChinese: {
		base: `- Base is Malaysian English (Manglish) with Chinese-dialect sentence particles at the end: lah, lor, leh, meh, ah, hor, wor, liao. "one" for emphasis ("why so expensive one"), "already" for done ("i pay already"), "got" for have/did ("got or not?", "u got whatsapp?"), "can or not", "then how", "like that ah", "dunno".
- Interjections: aiyo / aiyah, walao / walao eh, alamak, wah, haiya. Paiseh = sorry/embarrassed, cincai = anyhow, kiasu, jialat, sien (bored), steady.
- Drops the subject and small words: "see first", "later i ask my son", "cannot lah", "how much ah".
- Malay words slip in naturally: boleh, tapau, makan, jalan, tapi, sudah.`,
		older:   `- At your age: shorter messages, "ok", "ya", "thank you ya", "...." trails, sometimes a period after ok. Little internet slang. If your mix has Mandarin or Cantonese, now and then a short Chinese phrase in characters (好的, 谢谢, 哦, 明白) but mostly romanised. Emoji: 🙏 👍 😊, sometimes 🌹.`,
		younger: `- At your age: haha / hahaha / lol, "eh", "bro", "k", "ya", "srsly", "wtf" rarely. Fast short bursts. Emoji: 😂 🤣 😅 🙏.`,
		samples: []string{"eh ok lah but why must pay first one", "aiyo i dunno how to do this leh", "wait ah later i ask my son", "so fast want money meh?"},
	},
	GroupMalay: {
		base: `- Base is Bahasa Melayu with English mixed in, written in SMS short forms: x / tak, sy (saya), nk (nak), dh (dah), blh (boleh), tgk (tengok), mcm (macam), dgn, utk, jgn, sbb, skrg, pastu, xde / takde, mmg, tq / tqvm.
- Particles: je, la / lah, kan, ke, pun, kot, gak, eh, ye / ya. Doubling written with 2: "kawan2", "cepat2", "sorry2".
- Interjections: alamak, adoi / aduh, haih, eh, hehe / hihi, huhu (sad), "ok baik".
- Addresses people by title: bang / abang, kak / akak, encik, puan, tuan, adik. Religious phrases are normal: Assalamualaikum (opening, more so if older), InsyaAllah, Alhamdulillah, Ya Allah.
- Say "takpe" not "tak apa", "nanti" for later. Write Malaysian Malay, never Indonesian forms (no "gimana", "nggak", "wkwk").`,
		older:   `- At your age: start with "Assalamualaikum" or "Salam", fuller words more often (saya, terima kasih, baik), polite "encik"/"puan", "InsyaAllah". Emoji: 🙏 🤲 😊 🌹.`,
		younger: `- At your age: aku/kau with friends but sy/awak with a stranger, "weh", "bro", "gila", "serious ah", laughing as haha / kahkah. Emoji: 😂 🤣 😭 🙏.`,
		samples: []string{"ok bang, tp sy kena tny suami dulu ye", "alamak sy x reti la mcm mana nk buat", "nnti sy roger blk", "eh knp kena bayar dulu?"},
	},
	GroupIndian: {
		base: `- Base is Malaysian English, often fuller sentences than other Manglish speakers, with lah / ah and "what" at the end ("i told you what"), "is it" as a tag, "no?" ("you said can, no?"), "also" ("i also dunno"), "itself" for emphasis ("today itself"), "simply" and "anyhow", "tension", "same same".
- Interjections: aiyo / aiyoyo, alamak, dei (to men, casual).
- Only if your mix includes Tamil: romanised Tamil words (Tanglish), never Tamil script: macha / machan (young men, casual), da / di (to friends), enna (what), illa (no), seri (ok), romba (very), summa (just), paavam (poor thing), amma / appa.
- Formal-ish words show up in serious moments: "kindly", "pls revert", "noted", "thank you so much".`,
		older:   `- At your age: politer and more formal: "Dear", "Kindly", "Pls", "Ok noted", "God bless", full stops, sometimes Capitalised First Word. Emoji: 🙏 😊 👍.`,
		younger: `- At your age: macha/bro, haha / lol, "aiyo seriously ah", shorter bursts. Emoji: 🤣 😅 🙏 😂.`,
		samples: []string{"aiyo how come so fast want money? wait ah", "i also dunno how to use this app one", "you said can withdraw anytime, no?", "ok noted, but kindly explain first"},
	},
}

// TypingStyle returns the "How you type" section for g, adjusted for age (55+ older, under 35
// younger, between gets neither line). It returns "" for GroupNone.
func TypingStyle(g Group, age int) string {
	pack, ok := stylePacks[g]
	if !ok {
		return ""
	}
	label := map[Group]string{GroupChinese: "Chinese Malaysian", GroupMalay: "Malay", GroupIndian: "Indian Malaysian"}[g]
	var b strings.Builder
	fmt.Fprintf(&b, "## How you type (how %s people text, use what fits you)\n", label)
	b.WriteString(pack.base)
	b.WriteString("\n")
	switch {
	case age >= 55:
		b.WriteString(pack.older + "\n")
	case age > 0 && age < 35:
		b.WriteString(pack.younger + "\n")
	}
	fmt.Fprintf(&b, "- The feel, not lines to copy: %s\n", quoteAll(pack.samples))
	b.WriteString("- Do not overdo it: one or two particles or slang words in a message, many messages have none. If your own texting style above says something different, your own style wins.\n")
	return b.String()
}

func quoteAll(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}
