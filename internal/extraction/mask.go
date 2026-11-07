package extraction

import (
	"regexp"
	"strings"
	"unicode"
)

// digitRun is 6+ digits, optionally broken by single spaces, dashes or dots ("1234 5678 90",
// "1234-56-7890"). Shorter numbers (amounts like "RM 1500", times) are left alone.
var digitRun = regexp.MustCompile(`\d(?:[ .\-]?\d){5,}`)

// MaskDigits hides account and phone numbers in a chat excerpt shown outside the operator console:
// each run of 6+ digits keeps its first 4 digits and the rest becomes "xxxx" ("Maybank 1234xxxx").
func MaskDigits(text string) string {
	return digitRun.ReplaceAllStringFunc(text, func(run string) string {
		var b strings.Builder
		n := 0
		for _, r := range run {
			if unicode.IsDigit(r) {
				if n < 4 {
					b.WriteRune(r)
				}
				n++
			}
		}
		return b.String() + "xxxx"
	})
}

// Excerpt masks text and cuts it to max runes, adding "…" when cut.
func Excerpt(text string, max int) string {
	r := []rune(strings.TrimSpace(MaskDigits(text)))
	if len(r) <= max {
		return string(r)
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}
