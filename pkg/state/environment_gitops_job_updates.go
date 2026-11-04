package state

import (
	"context"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// EnvironmentGitOpsJobUpdateStore stops schedule admission before an intent
// update waits for accepted tasks. Pausing commits separately from the update;
// existing runs keep their original release and frozen inputs until they drain.
type EnvironmentGitOpsJobUpdateStore interface {
	PrepareEnvironmentGitOpsJobUpdates(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) ([]EnvironmentGitOpsStep, error)
}

type gitOpsJobUpdate struct{ Resource, AppID, JobID string }

func gitOpsJobUpdates(snapshot gitOpsIntentSnapshot, plan environmentsync.Plan, ids map[string]string) []gitOpsJobUpdate {
	rows := changedWorkloadIntents(snapshot, plan, ids, false)
	targets := []gitOpsJobUpdate{}
	for _, resource := range snapshot.Resources {
		if row, changed := rows[resource.AppID]; changed && row.JobID != "" {
			targets = append(targets, gitOpsJobUpdate{Resource: resource.Resource, AppID: resource.AppID, JobID: row.JobID})
		}
	}
	slices.SortFunc(targets, func(a, b gitOpsJobUpdate) int {
		return strings.Compare(a.AppID, b.AppID)
	})
	return targets
}

func gitOpsJobUpdateStep(target gitOpsJobUpdate, status string, active bool) []EnvironmentGitOpsStep {
	if active {
		return []EnvironmentGitOpsStep{{Resource: target.Resource, Path: "schedule", Action: "drain", Status: "waiting"}}
	}
	if status == "active" {
		return []EnvironmentGitOpsStep{{Resource: target.Resource, Path: "schedule", Action: "pause", Status: "applied"}}
	}
	return nil
}
