package diagnostics

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Current reports identify the final observed intent, or the claimed intent
// when observation failed. Legacy empty plans cannot establish that identity.
func currentReport(status api.EnvironmentGitOpsStatusResponse, now time.Time) (*api.EnvironmentGitOpsRun, api.EnvironmentGitOpsPlan) {
	var latest *api.EnvironmentGitOpsRun
	var latestPlan api.EnvironmentGitOpsPlan
	for i := range status.Runs {
		run := &status.Runs[i]
		if run.SourceID != status.Source.ID || run.RevisionID != status.Source.ApprovedRevisionID || run.Generation != status.Source.Generation ||
			run.CompletedAt == nil || run.CompletedAt.After(now) || run.StartedAt.IsZero() || run.StartedAt.After(*run.CompletedAt) {
			continue
		}
		switch run.Status {
		case "blocked", "partial", "failed", "drifted", "overridden", "converged":
		default:
			continue
		}
		var plan api.EnvironmentGitOpsPlan
		if json.Unmarshal(run.Plan, &plan) != nil || plan.Manager != status.Source.ID || plan.Revision != status.Source.ApprovedRevisionID ||
			plan.Generation != status.Source.Generation || plan.ObservedVersion != status.Source.IntentVersion ||
			(plan.Hash == "" && run.Status != "failed" && run.Status != "partial") {
			continue
		}
		if plan.Hash != "" {
			if decoded, err := hex.DecodeString(plan.Hash); err != nil || len(decoded) != 32 {
				continue
			}
		}
		if latest == nil || run.StartedAt.After(latest.StartedAt) || run.StartedAt.Equal(latest.StartedAt) && run.ID > latest.ID {
			latest, latestPlan = run, plan
		}
	}
	return latest, latestPlan
}

// Blockers in plans contain field paths and human-readable details. Only
// known suffixes and validated workload names cross the diagnostics boundary.
func reportBlocker(blocker string) (string, string) {
	identity, reason, ok := strings.Cut(blocker, ": ")
	if !ok {
		return "", blocker // Explain maps unknown codes to a safe generic issue.
	}
	resource, _, _ := strings.Cut(identity, "#")
	if resource != "environment" {
		name, found := strings.CutPrefix(resource, "workload/")
		if !found || !api.ValidAppSlug(name) {
			resource = ""
		}
	}
	code := "unrecognized_blocker"
	switch reason {
	case "adoption required":
		code = "environment_adoption_required"
	case "ownership conflict":
		code = "environment_ownership_conflict"
	case "pruning is disabled":
		code = "environment_pruning_disabled"
	case "variable count exceeds plan quota across environments", "variable exceeds plan value limit", "policy count exceeds plan quota":
		code = "environment_plan_quota_exceeded"
	case "a current secret reference source is unavailable in this environment", "referenced secret is unavailable in this environment":
		code = "environment_secret_reference_unavailable"
	}
	return resource, code
}
