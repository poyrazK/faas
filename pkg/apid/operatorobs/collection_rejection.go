package operatorobs

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// Assess the already-read source, never a later live health read. The complete
// source is counted before the sanitized unhealthy preview is bounded.
func collectionRejection(groups []promql.AlertRuleGroup, now time.Time) api.ObsAlertCollectionRejection {
	health := assessRuleHealth(groups, api.ObsRuleHealthResponse{Items: []api.ObsRuleHealthItem{}, Limit: 2 * api.ObsAlertSnapshotMaxRules}, now)
	out := api.ObsAlertCollectionRejection{Scope: "rejected_snapshot", Reason: "collector_validation_failed", Totals: *health.Totals, Items: []api.ObsRuleHealthItem{}, Limit: api.ObsAlertCollectionRejectionItemsMax}
	for _, item := range health.Items {
		if item.State == "healthy" {
			continue
		}
		out.TotalItems++
		if len(out.Items) < out.Limit {
			out.Items = append(out.Items, item)
		}
	}
	if out.TotalItems > 0 {
		out.Reason = "rule_diagnostics_present"
	} else if out.Totals.Rules == 0 && out.Totals.InvalidGroups == 0 {
		out.Reason = "no_alert_rules_observed"
	}
	// Long safe names/runbooks can fill the byte budget before the item cap.
	for {
		out.Truncated = out.TotalItems > len(out.Items)
		encoded, err := json.Marshal(out)
		if err == nil && len(encoded) <= api.ObsAlertCollectionRejectionMaxBytes {
			return out
		}
		if len(out.Items) == 0 {
			return out
		}
		out.Items = out.Items[:len(out.Items)-1]
	}
}
