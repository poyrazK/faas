package issues

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// FrequencyRuleHash preserves cooldowns through policy reordering and enable toggles.
// Changes to the rule's matching or timing semantics start a new alert cycle.
func FrequencyRuleHash(rule api.IssueFrequencyAlertRule) string {
	rule.Enabled = true
	raw, _ := json.Marshal(rule)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func FrequencyRuleMatches(rule api.IssueFrequencyAlertRule, issue api.Issue, occurred, now time.Time) bool {
	if !rule.Enabled || (rule.Environment != "" && rule.Environment != issue.Environment) {
		return false
	}
	if issue.State == "ignored" && (issue.IgnoredUntil == nil || issue.IgnoredUntil.After(now)) {
		return false
	}
	return occurred.After(now.Add(-time.Duration(rule.WindowSeconds)*time.Second)) && !occurred.After(now)
}

func FrequencyAlertDue(rule api.IssueFrequencyAlertRule, count int64, last *time.Time, now time.Time) bool {
	return count >= rule.MinimumEvents && (last == nil || !now.Before(last.Add(time.Duration(rule.CooldownSeconds)*time.Second)))
}
