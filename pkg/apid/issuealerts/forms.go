package issuealerts

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// FormUpdate edits one named rule while retaining the rest of the policy.
func FormUpdate(current api.IssueFrequencyAlertPolicy, form url.Values) (api.UpdateIssueFrequencyAlertPolicyRequest, error) {
	revision, err := strconv.ParseInt(form.Get("expected_revision"), 10, 64)
	if err != nil || revision < 0 {
		return api.UpdateIssueFrequencyAlertPolicyRequest{}, fmt.Errorf("invalid policy revision; reload the issues page")
	}
	out := api.UpdateIssueFrequencyAlertPolicyRequest{ExpectedRevision: &revision, Rules: append([]api.IssueFrequencyAlertRule{}, current.Rules...)}
	key := form.Get("key")
	index := -1
	for i, r := range out.Rules {
		if r.Key == key {
			index = i
			break
		}
	}
	switch form.Get("action") {
	case "remove":
		if index < 0 {
			return out, fmt.Errorf("rule no longer exists; reload the issues page")
		}
		out.Rules = append(out.Rules[:index], out.Rules[index+1:]...)
	case "save":
		minimum, e1 := strconv.ParseInt(form.Get("minimum_events"), 10, 64)
		window, e2 := strconv.ParseInt(form.Get("window_seconds"), 10, 64)
		cooldown, e3 := strconv.ParseInt(form.Get("cooldown_seconds"), 10, 64)
		if e1 != nil || e2 != nil || e3 != nil {
			return out, fmt.Errorf("threshold, window and cooldown must be whole numbers")
		}
		rule := api.IssueFrequencyAlertRule{Key: key, Enabled: form.Get("enabled") == "on", Environment: form.Get("environment"), MinimumEvents: minimum, WindowSeconds: window, CooldownSeconds: cooldown}
		if index < 0 {
			out.Rules = append(out.Rules, rule)
		} else {
			out.Rules[index] = rule
		}
	default:
		return out, fmt.Errorf("invalid frequency alert action")
	}
	out.Rules, err = api.NormalizeIssueFrequencyAlertRules(out.Rules)
	return out, err
}
