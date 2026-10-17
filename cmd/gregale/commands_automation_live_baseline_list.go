// adr: 1304
package main

import (
	"context"
	"fmt"
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

func runAutomationLiveBaselineList(ctx context.Context, client *api.Client, o automationLiveBaselineOptions) int {
	view, err := client.ListAutomationLiveBaselines(ctx, o.app, o.name)
	if err != nil {
		return printErr("Could not list baselines", err)
	}
	if view.ObservedAt.IsZero() || len(view.Baselines) > api.AutomationLiveBaselineLimit {
		return invalidAutomationLiveBaselineListing()
	}
	seen := map[string]bool{}
	appID := ""
	for _, entry := range view.Baselines {
		receipt := entry.Baseline
		if _, err := validateAutomationLiveBaselineReceipt(receipt, o.name, receipt.BaselineName); err != nil {
			return invalidAutomationLiveBaselineListing()
		}
		if seen[receipt.BaselineName] || (appID != "" && receipt.AppID != appID) || len(entry.Cases) > api.AutomationLiveSuiteMaxCases || !validBaselineHealth(entry) {
			return invalidAutomationLiveBaselineListing()
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
	if err := writeAutomationBaselineListing("Observed %s. Evidence health and renewal readiness are not CI gate verdicts; historical targets and baseline age do not expire a CI baseline.\n", view.ObservedAt.UTC().Format("2006-01-02 15:04:05 UTC")); err != nil {
		return printErr("Could not write baselines", err)
	}
	for _, entry := range view.Baselines {
		if err := writeAutomationBaselineListing("%s\trevision %d\tsuite %s\tevidence %s\trenewal %s\n", entry.Baseline.BaselineName, entry.Baseline.Revision, entry.Baseline.Suite.ID, entry.EvidenceStatus, entry.RenewalStatus); err != nil {
			return printErr("Could not write baselines", err)
		}
		for _, reason := range entry.Reasons {
			if err := writeAutomationBaselineListing("  %s\n", reason); err != nil {
				return printErr("Could not write baselines", err)
			}
		}
	}
	return 0
}
func validBaselineHealth(entry api.AutomationLiveBaselineHealth) bool {
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
func writeAutomationBaselineListing(format string, args ...any) error {
	_, err := fmt.Fprintf(osStdout, format, args...)
	return err
}

func invalidAutomationLiveBaselineListing() int {
	return printErr("Invalid baseline listing", &exitErr{msg: "The server response did not contain consistent baseline identities or evidence health.", code: 3})
}
