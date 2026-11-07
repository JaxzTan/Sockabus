package telegram

import (
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestDescribeSentCode(t *testing.T) {
	for typ, want := range map[tg.AuthSentCodeTypeClass]string{
		&tg.AuthSentCodeTypeApp{}:                                      "Telegram app",
		&tg.AuthSentCodeTypeSMS{}:                                      "by SMS",
		&tg.AuthSentCodeTypeEmailCode{EmailPattern: "j***@gmail.com"}:  "j***@gmail.com",
		&tg.AuthSentCodeTypeSetUpEmailRequired{}:                       "login email",
		&tg.AuthSentCodeTypeFragmentSMS{URL: "https://fragment.com/x"}: "fragment.com/x",
	} {
		if got := DescribeSentCode(&tg.AuthSentCode{Type: typ}); !strings.Contains(got, want) {
			t.Errorf("%T: %q does not mention %q", typ, got, want)
		}
	}
}
