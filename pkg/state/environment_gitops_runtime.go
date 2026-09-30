package state

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// Runtime observations never contain customer values. RequiredAt comes from
// committed variable rows, the snapshot freshness stamp, and pending work;
// retries must not replace it with the current clock.
type EnvironmentGitOpsRuntimeTarget struct {
	AppID             string
	Resource          string
	Environment       string
	RequiredAt        time.Time
	StaleResidents    int64
	StartingResidents int64
	StaleSnapshots    int64
}

func (t EnvironmentGitOpsRuntimeTarget) Ready() bool {
	return t.StaleResidents == 0 && t.StartingResidents == 0 && t.StaleSnapshots == 0
}

type EnvironmentGitOpsRuntimeEffect struct {
	ID            string
	SourceID      string
	Generation    int64
	PlanHash      string
	AppID         string
	Environment   string
	RequiredAt    time.Time
	WakeID        string
	NextRequestAt time.Time
	RequestedAt   *time.Time
	CompletedAt   *time.Time
}

type EnvironmentGitOpsRuntimeRequest struct {
	AppID  string `json:"app_id"`
	WakeID string `json:"wake_id"`
	Scope  string `json:"scope"`
}

type EnvironmentGitOpsRuntimeProgress struct {
	Ready bool
	// PgStore enqueues within the effect transaction and returns nil here.
	// The in-memory adapter returns its handoff for the controller's notifier.
	Request *EnvironmentGitOpsRuntimeRequest
}

type EnvironmentGitOpsRuntimeStore interface {
	ObserveEnvironmentGitOpsRuntime(context.Context, EnvironmentGitOpsLease) ([]EnvironmentGitOpsRuntimeTarget, error)
	EnsureEnvironmentGitOpsRuntime(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) error
	PendingEnvironmentGitOpsRuntime(context.Context, EnvironmentGitOpsLease) ([]EnvironmentGitOpsRuntimeEffect, error)
	ReconcileEnvironmentGitOpsRuntime(context.Context, EnvironmentGitOpsLease, string) (EnvironmentGitOpsRuntimeProgress, error)
}

func gitOpsRuntimeReady(targets []EnvironmentGitOpsRuntimeTarget) bool {
	for _, target := range targets {
		if !target.Ready() {
			return false
		}
	}
	return true
}

func gitOpsChangedVariableApps(plan environmentsync.Plan, ids map[string]string) []string {
	seen := map[string]bool{}
	apps := []string{}
	for _, change := range plan.Changes {
		if strings.HasPrefix(change.Path, "variables/") &&
			(change.Action == "create" || change.Action == "update" || change.Action == "remove") {
			app := ids[change.Resource]
			if !seen[app] {
				apps = append(apps, app)
				seen[app] = true
			}
		}
	}
	return apps
}
