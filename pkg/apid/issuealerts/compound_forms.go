package issuealerts

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func CompoundFormUpdate(current api.IssueCompoundAlertPolicy, form url.Values) (api.UpdateIssueCompoundAlertPolicyRequest, error) {
	revision, err := strconv.ParseInt(form.Get("expected_revision"), 10, 64)
	if err != nil || revision < 0 {
		return api.UpdateIssueCompoundAlertPolicyRequest{}, fmt.Errorf("invalid policy revision; reload compound alerts")
	}
	out := api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &revision, Rules: append([]api.IssueCompoundAlertRule{}, current.Rules...)}
	index := -1
	for i, rule := range out.Rules {
		if rule.Key == form.Get("key") {
			index = i
		}
	}
	if form.Get("action") == "remove" {
		if index < 0 {
			return out, fmt.Errorf("rule no longer exists; reload compound alerts")
		}
		out.Rules = append(out.Rules[:index], out.Rules[index+1:]...)
	} else if form.Get("action") == "save" || form.Get("action") == "preview" {
		rule, err := CompoundRuleForm(form)
		if err != nil {
			return out, err
		}
		if index < 0 {
			out.Rules = append(out.Rules, rule)
		} else {
			out.Rules[index] = rule
		}
	} else {
		return out, fmt.Errorf("invalid compound alert action")
	}
	out.Rules, err = api.NormalizeIssueCompoundAlertRules(out.Rules)
	return out, err
}

func CompoundRuleForm(form url.Values) (api.IssueCompoundAlertRule, error) {
	rule := api.IssueCompoundAlertRule{Key: form.Get("key"), Environment: form.Get("environment"), Enabled: form.Get("enabled") == "on"}
	for key, target := range map[string]*int64{"minimum_events": &rule.MinimumEvents, "minimum_customers": &rule.MinimumCustomers, "window_seconds": &rule.WindowSeconds, "cooldown_seconds": &rule.CooldownSeconds} {
		value, err := strconv.ParseInt(form.Get(key), 10, 64)
		if err != nil {
			return rule, fmt.Errorf("thresholds, window and cooldown must be whole numbers")
		}
		*target = value
	}
	_, err := api.NormalizeIssueCompoundAlertRules([]api.IssueCompoundAlertRule{rule})
	return rule, err
}
