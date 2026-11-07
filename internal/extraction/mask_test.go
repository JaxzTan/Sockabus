package extraction

import (
	"regexp"
	"testing"
)

func TestMaskDigits(t *testing.T) {
	for in, want := range map[string]string{
		"ok sis bank in to this acc first, Maybank 1234567890": "ok sis bank in to this acc first, Maybank 1234xxxx",
		"acc 5621 0384 11 cimb":                                "acc 5621xxxx cimb",
		"call 012-345 6789 ya":                                 "call 0123xxxx ya",
		"tng 1234.5678.90":                                     "tng 1234xxxx",
		"pay RM 1500 by 10.30 pm":                              "pay RM 1500 by 10.30 pm",
		"12345 is short":                                       "12345 is short",
		"":                                                     "",
	} {
		if got := MaskDigits(in); got != want {
			t.Errorf("MaskDigits(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExcerptNeverLeaksLongDigitRuns(t *testing.T) {
	long := regexp.MustCompile(`\d{6,}`)
	got := Excerpt("transfer to 800122334455 now and also 0123456789 then wallet 0x1234567890abcdef1234567890abcdef12345678 ok", 60)
	if long.MatchString(got) {
		t.Fatalf("excerpt leaks a digit run: %q", got)
	}
	if n := len([]rune(got)); n > 60 {
		t.Fatalf("excerpt is %d runes, want <= 60", n)
	}
}
