package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod 2026-11-22: test apps' migrations that exited 1 or 3 counted as
// platform deployment failures. A release command's own failure is the
// customer's; exit 126/127 (not executable, not found) stays platform
// evidence, as when the runner base lost npm.
func TestDeploymentOutcomesAttributeReleaseCommandExit(t *testing.T) {
	m := NewMemStore()
	now := time.Now()
	m.mu.Lock()
	m.apps["app-1"] = App{ID: "app-1", AccountID: "acct-1"}
	for i, exit := range []*int{exitCode(1), exitCode(3), nil, exitCode(127)} {
		id := []string{"d-exit1", "d-exit3", "d-timeout", "d-notfound"}[i]
		m.putDeploymentLocked(id, Deployment{ID: id, AppID: "app-1", Status: DeployFailed, ErrorCode: api.CodeReleaseCommandFailed, CreatedAt: now})
		m.appTasks["t-"+id] = AppTask{ID: "t-" + id, AppID: "app-1", DeploymentID: id, Kind: AppTaskKindRelease, Status: AppTaskFailed, ExitCode: exit}
	}
	m.mu.Unlock()

	counts, err := m.CountDeploymentOutcomesSince(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if counts.Failed != 1 || counts.FailedAccounts != 1 {
		t.Fatalf("outcomes = %+v, want only the exit-127 release as a platform failure", counts)
	}
}

func exitCode(v int) *int { return &v }
