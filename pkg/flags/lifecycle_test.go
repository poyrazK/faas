package flags

import (
	"slices"
	"testing"
	"time"
)

func intRef(v int) *int { return &v }

// ADR 20261114150000 §4.
func TestLifecycleReport(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	old := Flag{Key: "old-launch", Enabled: true, Default: true, Owner: "growth"}
	active := Flag{Key: "pilot", Enabled: true, Owner: "payments", ExpiresAt: "2026-11-18", Rules: []Rule{{ID: "p", Customers: []string{"c"}, Value: true}}}
	expired := Flag{Key: "expired", Enabled: true, ExpiresAt: "2026-11-13", Rules: []Rule{{ID: "half", Rollout: intRef(5000), Value: true}}}
	history := []HistoryVersion{
		{Version: 5, CreatedAt: now.Add(-1 * day), Config: Config{Flags: []Flag{old, active, expired}}},
		{Version: 4, CreatedAt: now.Add(-2 * day), Config: Config{Flags: []Flag{old, active}}},
		{Version: 3, CreatedAt: now.Add(-20 * day), Config: Config{Flags: []Flag{old, {Key: "pilot", Enabled: false}}}},
		{Version: 2, CreatedAt: now.Add(-30 * day), Config: Config{Flags: []Flag{old}}},
		{Version: 1, CreatedAt: now.Add(-40 * day), Config: Config{Flags: []Flag{{Key: "old-launch", Enabled: true}}}},
	}
	rows := Lifecycle(history, map[string]int64{"old-launch": 12, "pilot": 0}, now)
	byKey := map[string]FlagLifecycle{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	if r := byKey["old-launch"]; r.LastChangedVersion != 2 || !r.ServesOneValue || !slices.Equal(r.Attention, []string{AttentionStale}) || *r.EvaluatedRequests != 12 {
		t.Errorf("old-launch = %+v", r)
	}
	// Changed 2 days ago: not stale and too new to call never evaluated.
	if r := byKey["pilot"]; r.LastChangedVersion != 4 || r.ServesOneValue || !slices.Equal(r.Attention, []string{AttentionExpiresSoon}) {
		t.Errorf("pilot = %+v", r)
	}
	if r := byKey["expired"]; r.LastChangedVersion != 5 || !slices.Equal(r.Attention, []string{AttentionExpired, AttentionNoOwner}) {
		t.Errorf("expired = %+v", r)
	}

	// Evidence unavailable: no never_evaluated, evidence says so.
	rows = Lifecycle(history[2:], nil, now)
	for _, r := range rows {
		if r.Evidence != "unavailable" || r.EvaluatedRequests != nil || slices.Contains(r.Attention, AttentionNeverEvaluated) {
			t.Errorf("without evidence: %+v", r)
		}
	}
	// An enabled flag unchanged for a week with zero requests.
	rows = Lifecycle(history[2:], map[string]int64{}, now)
	for _, r := range rows {
		if r.Key == "old-launch" && !slices.Contains(r.Attention, AttentionNeverEvaluated) {
			t.Errorf("never evaluated missed: %+v", r)
		}
		if r.Key == "pilot" && slices.Contains(r.Attention, AttentionNeverEvaluated) {
			t.Errorf("disabled flag flagged never evaluated: %+v", r)
		}
	}
	if got := Lifecycle(nil, nil, now); len(got) != 0 {
		t.Fatalf("empty history = %v", got)
	}
}

func TestServesOneValue(t *testing.T) {
	cases := map[string]struct {
		f    Flag
		want bool
	}{
		"disabled":          {Flag{Key: "a", Rules: []Rule{{ID: "r", Customers: []string{"c"}}}}, true},
		"killed":            {Flag{Key: "a", Enabled: true, Killed: true, Rules: []Rule{{ID: "r", Customers: []string{"c"}}}}, true},
		"no rules":          {Flag{Key: "a", Enabled: true}, true},
		"everyone 100%":     {Flag{Key: "a", Enabled: true, Rules: []Rule{{ID: "r", Rollout: intRef(10000), Value: true}}}, true},
		"partial rollout":   {Flag{Key: "a", Enabled: true, Rules: []Rule{{ID: "r", Rollout: intRef(5000), Value: true}}}, false},
		"targeted":          {Flag{Key: "a", Enabled: true, Rules: []Rule{{ID: "r", Group: "g", Value: true}}}, false},
		"weighted variants": {Flag{Key: "a", Type: "variant", Enabled: true, Rules: []Rule{{ID: "r", Rollout: intRef(10000)}}}, false},
	}
	for name, c := range cases {
		if got := ServesOneValue(c.f); got != c.want {
			t.Errorf("%s: ServesOneValue = %v, want %v", name, got, c.want)
		}
	}
}

func TestValidateOwnerAndExpiry(t *testing.T) {
	base := func(owner, expires string) Config {
		return Config{Flags: []Flag{{Key: "f", Enabled: true, Default: false, Seed: "s", Owner: owner, ExpiresAt: expires, Rules: []Rule{}}}}
	}
	if err := Validate(base("team-payments@example.com", "2027-01-31")); err != nil {
		t.Fatalf("valid owner/expiry rejected: %v", err)
	}
	for name, cfg := range map[string]Config{
		"control char owner": base("a\nb", ""),
		"long owner":         base(string(make([]byte, 129)), ""),
		"bad date":           base("", "31/01/2027"),
	} {
		if err := Validate(cfg); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
