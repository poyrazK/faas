// adr: 1662
package api

import "testing"

func TestNormalizeIssueCompoundAlertRules(t *testing.T) {
	rule := IssueCompoundAlertRule{Key: "payments", Enabled: true, MinimumEvents: 20, MinimumCustomers: 3, WindowSeconds: 600, CooldownSeconds: 1800}
	for _, mutate := range []func(*IssueCompoundAlertRule){
		func(r *IssueCompoundAlertRule) { r.MinimumCustomers = 0 }, func(r *IssueCompoundAlertRule) { r.MinimumCustomers = IssueCompoundAlertMaxCustomers + 1 },
		func(r *IssueCompoundAlertRule) { r.MinimumEvents = 0 }, func(r *IssueCompoundAlertRule) { r.WindowSeconds = 59 }, func(r *IssueCompoundAlertRule) { r.CooldownSeconds = 0 },
		func(r *IssueCompoundAlertRule) { r.Key = "Bad key" }, func(r *IssueCompoundAlertRule) { r.Environment = "prod*" },
	} {
		invalid := rule
		mutate(&invalid)
		if _, err := NormalizeIssueCompoundAlertRules([]IssueCompoundAlertRule{invalid}); err == nil {
			t.Fatal("invalid rule accepted", invalid)
		}
	}
	if _, err := NormalizeIssueCompoundAlertRules([]IssueCompoundAlertRule{rule, rule}); err == nil {
		t.Fatal("duplicate key accepted")
	}
	if _, err := NormalizeIssueCompoundAlertRules(make([]IssueCompoundAlertRule, IssueCompoundAlertMaxRules+1)); err == nil {
		t.Fatal("too many rules accepted")
	}
	other := rule
	other.Key = "alpha"
	input := []IssueCompoundAlertRule{rule, other}
	out, err := NormalizeIssueCompoundAlertRules(input)
	if err != nil || out[0].Key != "alpha" || input[0].Key != "payments" {
		t.Fatal(out, err)
	}
}
