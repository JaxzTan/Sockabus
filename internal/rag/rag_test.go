package rag

import (
	"strings"
	"testing"
)

func msgs() []Message {
	in := func(c int64, t string) Message {
		return Message{ConversationID: c, PersonaID: c, ScamType: "investment", Inbound: true, Text: t}
	}
	out := func(c int64, t string) Message {
		return Message{ConversationID: c, PersonaID: c, ScamType: "investment", Text: t}
	}
	return []Message{
		out(1, "hello?"), // we spoke first: no pair
		in(1, "please transfer RM500 deposit to activate your account"),
		out(1, "ok but which bank ah"), out(1, "account name what?"),
		in(1, "Maybank 5123 4567 8901 name Ace Capital"),
		out(1, "the maybank one ending 8901 ah? my app say error, got other account?"),
		in(2, "transfer the deposit now, RM500 only"),
		out(2, "aiyo my bank app hang, can give other bank account"),
		in(3, "hi dear, how are you today"),
		out(3, "fine, who is this"),
		in(4, "send deposit fast"),
		out(4, "see wa.me/60123456789 or @ace_admin"),
	}
}

func TestBuildPairsAndSanitizes(t *testing.T) {
	pairs := Build(msgs())
	if len(pairs) != 5 {
		t.Fatalf("got %d pairs: %+v", len(pairs), pairs)
	}
	if pairs[0].Reply != "ok but which bank ah / account name what?" {
		t.Errorf("bubbles not joined: %q", pairs[0].Reply)
	}
	for _, p := range pairs {
		if strings.ContainsAny(p.Reply, "0123456789") || strings.Contains(p.Reply, "@ace") || strings.Contains(p.Reply, "wa.me") {
			t.Errorf("reply leaks another chat's details: %q", p.Reply)
		}
	}
}

func TestSearchRanksExcludesAndBoosts(t *testing.T) {
	var ix Index
	ix.Replace(Build(msgs()))
	got := ix.Search(Query{Text: "pls transfer deposit RM500 now", ConversationID: 2}, 3)
	if len(got) == 0 || got[0].ConversationID != 1 {
		t.Fatalf("want conversation 1 first, got %+v", got)
	}
	for _, p := range got {
		if p.ConversationID == 2 {
			t.Error("current conversation must be excluded")
		}
	}
	if ix.Search(Query{Text: "nasi lemak sedap"}, 3) != nil {
		t.Error("unrelated text must return nothing")
	}
	// Same text, same-group pair wins over an equally relevant other-group pair.
	ix.Replace([]Pair{
		{ConversationID: 10, Group: "chinese", Scammer: "transfer deposit now", Reply: "which bank ah"},
		{ConversationID: 11, Group: "malay", Scammer: "transfer deposit now", Reply: "bank apa ye"},
		{ConversationID: 12, Scammer: "unrelated words here", Reply: "filler one"},
		{ConversationID: 13, Scammer: "more filler text", Reply: "filler two"},
	})
	if got := ix.Search(Query{Text: "transfer deposit", Group: "malay"}, 1); len(got) != 1 || got[0].ConversationID != 11 {
		t.Errorf("group boost: %+v", got)
	}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"ok send to 1234-5678-90 pls":    "ok send to … pls",
		"the cimb ending 4421 ah?":       "the cimb ending … ah?",
		"check www.scam.com and @bob ya": "check [link] and [handle] ya",
		"123 456":                        "",
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	if r := []rune(Sanitize(strings.Repeat("ayam ", 100))); len(r) > MaxReply+1 {
		t.Errorf("not capped: %d runes", len(r))
	}
}
