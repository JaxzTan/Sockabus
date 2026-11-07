package risk

import (
	"reflect"
	"testing"
)

func TestAssess(t *testing.T) {
	for _, c := range []struct {
		name                    string
		typ, status, confidence string
		sightings               int32
		want                    Result
	}{
		{"verified single", "bank_account", "verified", "high", 1, Result{0.90, ReasonAccount, []string{SignalVerified}, ActionHold}},
		{"verified repeat", "bank_account", "verified", "high", 3, Result{0.94, ReasonAccount, []string{SignalVerified, SignalRepeat}, ActionHold}},
		{"verified capped", "wallet", "verified", "high", 50, Result{0.98, ReasonAccount, []string{SignalVerified, SignalRepeat}, ActionHold}},
		{"unverified medium never holds", "bank_account", "unverified", "medium", 9, Result{0.78, ReasonAccount, []string{SignalRepeat}, ActionWarn}},
		{"unverified low", "phone", "unverified", "low", 1, Result{0.50, ReasonContact, []string{}, ActionMonitor}},
		{"url", "url", "unverified", "medium", 1, Result{0.70, ReasonLink, []string{}, ActionWarn}},
		{"handle", "handle", "verified", "high", 1, Result{0.90, ReasonContact, []string{SignalVerified}, ActionHold}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Assess(c.typ, c.status, c.confidence, c.sightings); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Assess = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestAssessNeverFreezes(t *testing.T) {
	for _, conf := range []string{"high", "medium", "low", ""} {
		for _, st := range []string{"verified", "unverified", ""} {
			a := Assess("bank_account", st, conf, 100).Action
			if a != ActionHold && a != ActionWarn && a != ActionMonitor {
				t.Fatalf("unexpected action %q", a)
			}
		}
	}
}
