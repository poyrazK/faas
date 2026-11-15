package main

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func obsDeploymentServingEvidence(out api.ObsServingHealth, after, now time.Time) []obsDeploymentEvidenceDetail {
	items := []obsDeploymentEvidenceDetail{}
	for _, item := range []obsDeploymentEvidenceDetail{
		obsDeploymentEvidenceClock("serving_snapshot", &out.GeneratedAt, api.ObsHealthFreshnessMaxAge.Seconds(), false, after, now),
		obsDeploymentEvidenceClock("serving_inventory", out.Inventory.ObservedAt, api.ObsHealthFreshnessMaxAge.Seconds(), false, after, now),
	} {
		if item.Reason != "" {
			items = append(items, item)
		}
	}
	if len(items) > 0 {
		return items
	}
	if out.Inventory.Configured == nil || !*out.Inventory.Configured || out.Counts == nil || out.Counts.Expected == 0 {
		reason := "expectations_unavailable"
		if out.Inventory.Configured != nil && !*out.Inventory.Configured {
			reason = "expectations_not_configured"
		}
		if out.Inventory.Configured != nil && *out.Inventory.Configured && out.Counts != nil && out.Counts.Expected == 0 {
			reason = "no_expected_probes"
		}
		return append(items, obsDeploymentEvidenceDetail{Kind: "serving_inventory", Reason: reason, SourceState: out.Inventory.State, SourceReason: out.Inventory.Reason})
	}
	for _, probe := range out.Probes {
		item := obsDeploymentEvidenceClock("probe_evaluation", probe.EvaluatedAt, api.ObsHealthFreshnessMaxAge.Seconds(), false, after, now)
		if item.Reason == "" {
			switch probe.State {
			case "unknown":
				item.Reason = "unknown_probe_state"
			case "healthy":
				item = obsDeploymentEvidenceClock("probe_run", probe.LastRunAt, probe.MaxResultAgeSeconds, true, after, now)
			}
		}
		if item.Reason != "" {
			item.Node, item.URLID, item.SourceState, item.SourceReason = probe.Node, probe.URLID, probe.State, probe.Reason
			items = append(items, item)
		}
	}
	return items
}
