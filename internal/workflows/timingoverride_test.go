package workflows

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestParseTimingOverrides(t *testing.T) {
	got, err := ParseTimingOverrides(" Auntie Mei:wpm=23,sleep=22-6,busyness=0.2,typo=0.12 ; 2:sleep=off,distracted=0.1;*:wpm=30;")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]PersonaTiming{
		"auntie mei": {WPM: 23, Sleep: []int{22, 6}, Busyness: 0.2, TypoRate: 0.12},
		"2":          {Sleep: []int{0, 0}, Distracted: 0.1},
		"*":          {WPM: 30},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if m, err := ParseTimingOverrides(""); err != nil || len(m) != 0 {
		t.Fatalf("empty: %v %v", m, err)
	}
	for _, bad := range []string{"wpm=20", "x:wpm=fast", "x:sleep=25-6", "x:typo=1.5", "x:speed=3", "x:wpm"} {
		if _, err := ParseTimingOverrides(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestResolvePersonaTiming(t *testing.T) {
	profile := json.RawMessage(`{"timing":{"wpm":23,"sleep":[22,6],"busyness":0.2,"typo_rate":0.12}}`)
	ov, _ := ParseTimingOverrides("*:distracted=0.3;auntie mei:wpm=18;7:sleep=off")

	got := ResolvePersonaTiming(profile, 7, "Auntie Mei", ov)
	want := PersonaTiming{WPM: 18, Sleep: []int{0, 0}, Busyness: 0.2, TypoRate: 0.12, Distracted: 0.3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if got.Asleep(time.Date(2026, 11, 4, 3, 0, 0, 0, myt)) {
		t.Fatal("sleep=off persona must never be asleep")
	}
	// No override: profile + defaults, same as ParsePersonaTiming.
	if a, b := ResolvePersonaTiming(profile, 1, "Ah Seng", nil), ParsePersonaTiming(profile); !reflect.DeepEqual(a, b) {
		t.Fatalf("%+v != %+v", a, b)
	}
}
