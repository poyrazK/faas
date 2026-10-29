package api

import (
	"slices"
	"strings"
	"testing"
)

// adr: 20261019102657 — channel rules match claims exactly and allow only a
// trailing wildcard.
func TestRealtimeChannelRules(t *testing.T) {
	claims := map[string]string{"sub": "user-1", "org_id": "org-7"}
	for _, tc := range []struct {
		pattern, channel string
		want             bool
	}{
		{"org:{org_id}:room:*", "org:org-7:room:lobby", true},
		{"org:{org_id}:room:*", "org:org-8:room:lobby", false},
		{"org:{org_id}:room:*", "org:org-7:feed", false},
		{"user:{sub}:inbox", "user:user-1:inbox", true},
		{"user:{sub}:inbox", "user:user-1:inbox2", false},
		{"user:{team_id}:inbox", "user::inbox", false}, // a missing claim never matches
		{"announcements", "announcements", true},
		{"announcements", "announcements-2", false},
	} {
		if got := MatchRealtimeChannelRule(RealtimeChannelRule{Pattern: tc.pattern, Read: true}, tc.channel, claims); got != tc.want {
			t.Errorf("%q vs %q = %v, want %v", tc.pattern, tc.channel, got, tc.want)
		}
	}
	// A claim value ending in * is literal text, not a wildcard.
	if MatchRealtimeChannelRule(RealtimeChannelRule{Pattern: "user:{sub}", Read: true}, "user:anything", map[string]string{"sub": "*"}) {
		t.Fatal("a claim value acted as a wildcard")
	}
	if got := RealtimeChannelRuleClaims("org:{org_id}:user:{sub}:*"); !slices.Equal(got, []string{"org_id", "sub"}) {
		t.Fatalf("claims = %v", got)
	}

	for _, tc := range []struct {
		rules   []RealtimeChannelRule
		wantErr string
	}{
		{[]RealtimeChannelRule{{Pattern: "org:{org_id}:*", Read: true}}, ""},
		{[]RealtimeChannelRule{{Pattern: "", Read: true}}, "1-256 bytes"},
		{[]RealtimeChannelRule{{Pattern: "a", Read: false}}, "must grant read"},
		{[]RealtimeChannelRule{{Pattern: "a*b", Read: true}}, "only end"},
		{[]RealtimeChannelRule{{Pattern: "org:{Org}", Read: true}}, "lowercase claim"},
		{[]RealtimeChannelRule{{Pattern: "*", Read: true}}, "auth none"},
		{make([]RealtimeChannelRule, RealtimeChannelRulesMax+1), "at most"},
	} {
		err := ValidateRealtimeChannelRules(tc.rules)
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("rules %v: err = %v, want %q", tc.rules, err, tc.wantErr)
		}
	}
}
