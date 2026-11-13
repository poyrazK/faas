// adr: 1304
package main

import (
	"context"
	"fmt"
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

func runStoredWorkflowLiveBaselineList(ctx context.Context, client *api.Client, o storedWorkflowLiveBaselineOptions) int {
	view, err := client.ListStoredWorkflowLiveBaselines(ctx, o.app, o.name)
	if err != nil {
		return printErr("Could not list baselines", err)
	}
	if view.ObservedAt.IsZero() || len(view.Baselines) > api.StoredWorkflowLiveBaselineLimit {
		return invalidStoredWorkflowLiveBaselineListing()
	}
	seen := map[string]bool{}
	appID := ""
	for _, entry := range view.Baselines {
		receipt := entry.Baseline
		if _, err := validateStoredWorkflowLiveBaselineReceipt(receipt, o.name, receipt.BaselineName); err != nil {
			return invalidStoredWorkflowLiveBaselineListing()
		}
		if seen[receipt.BaselineName] || (appID != "" && receipt.AppID != appID) || len(entry.Cases) > api.StoredWorkflowLiveSuiteMaxCases || !validBaselineHealth(entry) {
			return invalidStoredWorkflowLiveBaselineListing()
		}
		seen[receipt.BaselineName] = true
		appID = receipt.AppID
	}
	if jsonOutput {
		if err := writeJSON(view); err != nil {
			return printErr("Could not write baselines", err)
		}
		return 0
	}
	if err := writeStoredWorkflowBaselineListing("Observed %s. Evidence health and renewal readiness are not CI gate verdicts; historical targets and baseline age do not expire a CI baseline.\n", view.ObservedAt.UTC().Format("2006-01-02 15:04:05 UTC")); err != nil {
		return printErr("Could not write baselines", err)
	}
	for _, entry := range view.Baselines {
		if err := writeStoredWorkflowBaselineListing("%s\trevision %d\tsuite %s\tevidence %s\trenewal %s\n", entry.Baseline.BaselineName, entry.Baseline.Revision, entry.Baseline.Suite.ID, entry.EvidenceStatus, entry.RenewalStatus); err != nil {
			return printErr("Could not write baselines", err)
		}
		for _, reason := range entry.Reasons {
			if err := writeStoredWorkflowBaselineListing("  %s\n", reason); err != nil {
				return printErr("Could not write baselines", err)
			}
		}
	}
	return 0
}
func validBaselineHealth(entry api.StoredWorkflowLiveBaselineHealth) bool {
	if entry.EvidenceStatus != "usable" && entry.EvidenceStatus != "failed" && entry.EvidenceStatus != "unavailable" {
		return false
	}
	if entry.RenewalStatus != "passed" && entry.RenewalStatus != "failed" && entry.RenewalStatus != "inconclusive" {
		return false
	}
	if entry.AgeSeconds != nil && (math.IsNaN(*entry.AgeSeconds) || math.IsInf(*entry.AgeSeconds, 0)) {
		return false
	}
	for _, c := range entry.Cases {
		switch c.Status {
		case "passed", "failed", "pending", "unavailable", "not_started":
		default:
			return false
		}
	}
	return true
}
func writeStoredWorkflowBaselineListing(format string, args ...any) error {
	_, err := fmt.Fprintf(osStdout, format, args...)
	return err
}

func invalidStoredWorkflowLiveBaselineListing() int {
	return printErr("Invalid baseline listing", &exitErr{msg: "The server response did not contain consistent baseline identities or evidence health.", code: 3})
}
