package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObsCollectionRejectionSanitizedContract(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name   string
		change func(*api.ObsAlertCollectionRejection)
	}{
		{"safe", func(*api.ObsAlertCollectionRejection) {}},
		{"raw issue", func(d *api.ObsAlertCollectionRejection) { d.Items[0].Issues[0] = "private error" }},
		{"raw path", func(d *api.ObsAlertCollectionRejection) { d.Items[0].FileName = "/private/file" }},
		{"terminal name", func(d *api.ObsAlertCollectionRejection) { d.Items[0].RuleName = "Bad\nName" }},
		{"unsafe runbook", func(d *api.ObsAlertCollectionRejection) { d.Items[0].RunbookURL = "https://private.example/secret" }},
		{"encoded traversal", func(d *api.ObsAlertCollectionRejection) { d.Items[0].RunbookURL = "/docs/%2e%2e/private" }},
		{"long metadata", func(d *api.ObsAlertCollectionRejection) {
			d.Items[0].GroupName = strings.Repeat("a", api.ObsRuleHealthIdentityMaxBytes+1)
		}},
		{"bad hash", func(d *api.ObsAlertCollectionRejection) { d.Items[0].ID = "private identity" }},
		{"fabricated counts", func(d *api.ObsAlertCollectionRejection) { n := 0; d.Items[0].Firing = &n }},
		{"false priority", func(d *api.ObsAlertCollectionRejection) { d.Items[0].State = "stale" }},
		{"incorrect age", func(d *api.ObsAlertCollectionRejection) { n := int64(20); d.Items[0].EvaluationAgeSeconds = &n }},
		{"missing clock", func(d *api.ObsAlertCollectionRejection) { d.Items[0].LastEvaluatedAt = nil }},
		{"bad partition", func(d *api.ObsAlertCollectionRejection) { d.Totals.Healthy = 1 }},
		{"hidden items", func(d *api.ObsAlertCollectionRejection) { d.TotalItems = 0 }},
		{"incorrect truncation", func(d *api.ObsAlertCollectionRejection) { d.Truncated = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			age := int64(0)
			d := api.ObsAlertCollectionRejection{Scope: "rejected_snapshot", Reason: "rule_diagnostics_present", Totals: api.ObsRuleHealthTotals{Groups: 1, Rules: 1, Failed: 1}, Items: []api.ObsRuleHealthItem{{Kind: "rule", ID: strings.Repeat("a", 64), GroupID: strings.Repeat("b", 64), RuleName: "FaasApiDown", State: "failed", ReportedState: "inactive", Issues: []string{"evaluation_failed"}, LastEvaluatedAt: &now, EvaluationAgeSeconds: &age, HasEvaluationError: true, RunbookURL: "/docs/runbooks/FaasApiDown.md"}}, TotalItems: 1, Limit: api.ObsAlertCollectionRejectionItemsMax}
			tc.change(&d)
			if got := api.ValidObsAlertCollectionRejection(now, d); got != (tc.name == "safe") {
				t.Fatalf("validation=%t", got)
			}
		})
	}
}
