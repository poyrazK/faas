package issues

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFrequencyAlertEligibility(t *testing.T) {
	now := time.Now().UTC()
	future, past := now.Add(time.Minute), now.Add(-time.Minute)
	rule := api.IssueFrequencyAlertRule{Key: "burst", Enabled: true, Environment: "production", MinimumEvents: 2, WindowSeconds: 300, CooldownSeconds: 600}
	for _, tc := range []struct {
		name, env, state string
		until            *time.Time
		occurred         time.Time
		want             bool
	}{
		{"current", "production", "open", nil, now, true},
		{"window start excluded", "production", "open", nil, now.Add(-300 * time.Second), false},
		{"inside window", "production", "open", nil, now.Add(-300*time.Second + time.Nanosecond), true},
		{"future excluded", "production", "open", nil, now.Add(time.Nanosecond), false},
		{"other environment", "staging", "open", nil, now, false},
		{"ignored", "production", "ignored", &future, now, false},
		{"missing ignore deadline", "production", "ignored", nil, now, false},
		{"expired", "production", "ignored", &past, now, true},
		{"expiry boundary", "production", "ignored", &now, now, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FrequencyRuleMatches(rule, api.Issue{Environment: tc.env, State: tc.state, IgnoredUntil: tc.until}, tc.occurred, now); got != tc.want {
				t.Fatalf("match=%v want=%v", got, tc.want)
			}
		})
	}
	rule.Environment = ""
	if !FrequencyRuleMatches(rule, api.Issue{Environment: "staging"}, now, now) {
		t.Fatal("all-environment rule did not match")
	}
	rule.Enabled = false
	if FrequencyRuleMatches(rule, api.Issue{}, now, now) {
		t.Fatal("disabled rule matched")
	}
}
func TestFrequencyAlertCooldownAndHash(t *testing.T) {
	now := time.Now().UTC()
	rule := api.IssueFrequencyAlertRule{Key: "burst", Enabled: true, MinimumEvents: 2, WindowSeconds: 300, CooldownSeconds: 600}
	if FrequencyAlertDue(rule, 1, nil, now) || !FrequencyAlertDue(rule, 2, nil, now) {
		t.Fatal("threshold eligibility incorrect")
	}
	if FrequencyAlertDue(rule, 3, &now, now.Add(599*time.Second)) {
		t.Fatal("cooldown ended early")
	}
	if !FrequencyAlertDue(rule, 3, &now, now.Add(600*time.Second)) {
		t.Fatal("cooldown boundary did not rearm")
	}
	if FrequencyAlertDue(rule, 3, &now, now.Add(-time.Second)) {
		t.Fatal("clock reversal bypassed cooldown")
	}
	hash := FrequencyRuleHash(rule)
	rule.Enabled = false
	if FrequencyRuleHash(rule) != hash {
		t.Fatal("enable toggle reset cooldown")
	}
	for _, change := range []func(*api.IssueFrequencyAlertRule){
		func(r *api.IssueFrequencyAlertRule) { r.MinimumEvents++ }, func(r *api.IssueFrequencyAlertRule) { r.WindowSeconds++ }, func(r *api.IssueFrequencyAlertRule) { r.CooldownSeconds++ }, func(r *api.IssueFrequencyAlertRule) { r.Environment = "production" },
	} {
		copy := rule
		change(&copy)
		if FrequencyRuleHash(copy) == hash {
			t.Fatal("semantic change did not start a new cycle")
		}
	}
}
func TestFrequencyRuleValidation(t *testing.T) {
	valid := api.IssueFrequencyAlertRule{Key: "burst", Enabled: true, MinimumEvents: 2, WindowSeconds: 60, CooldownSeconds: 60}
	for _, change := range []func(*api.IssueFrequencyAlertRule){
		func(r *api.IssueFrequencyAlertRule) { r.Key = "" }, func(r *api.IssueFrequencyAlertRule) { r.Key = "Bad" }, func(r *api.IssueFrequencyAlertRule) { r.MinimumEvents = 0 }, func(r *api.IssueFrequencyAlertRule) { r.MinimumEvents = api.IssueFrequencyAlertMaxEvents + 1 },
		func(r *api.IssueFrequencyAlertRule) { r.WindowSeconds = 59 }, func(r *api.IssueFrequencyAlertRule) { r.WindowSeconds = 86401 }, func(r *api.IssueFrequencyAlertRule) { r.CooldownSeconds = 59 }, func(r *api.IssueFrequencyAlertRule) { r.CooldownSeconds = 604801 }, func(r *api.IssueFrequencyAlertRule) { r.Environment = "../prod" },
	} {
		bad := valid
		change(&bad)
		if _, err := api.NormalizeIssueFrequencyAlertRules([]api.IssueFrequencyAlertRule{bad}); err == nil {
			t.Fatalf("accepted invalid rule %+v", bad)
		}
	}
	if _, err := api.NormalizeIssueFrequencyAlertRules([]api.IssueFrequencyAlertRule{valid, valid}); err == nil {
		t.Fatal("duplicate keys accepted")
	}
	if _, err := api.NormalizeIssueFrequencyAlertRules(make([]api.IssueFrequencyAlertRule, api.IssueFrequencyAlertMaxRules+1)); err == nil {
		t.Fatal("oversized policy accepted")
	}
	last := valid
	last.Key = "a"
	input := []api.IssueFrequencyAlertRule{valid, last}
	out, err := api.NormalizeIssueFrequencyAlertRules(input)
	if err != nil || out[0].Key != "a" || input[0].Key != "burst" {
		t.Fatalf("normalization mutated caller or failed: %+v %v", out, err)
	}
	valid.MinimumEvents = api.IssueFrequencyAlertMaxEvents
	valid.WindowSeconds = api.IssueFrequencyAlertMaxWindowSeconds
	valid.CooldownSeconds = api.IssueFrequencyAlertMaxCooldownSeconds
	if _, err := api.NormalizeIssueFrequencyAlertRules([]api.IssueFrequencyAlertRule{valid}); err != nil {
		t.Fatal(err)
	}
}
