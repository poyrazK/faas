package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod 2026-11-22: test apps' migrations that exited 1 or 3 counted as
// platform deployment failures. A release command's own failure (an exit,
// its own timeout, its owner's cancel) is the customer's; exit 126/127 (not
// executable, not found) and a task that ended with neither stay platform
// evidence, as when the runner base lost npm.
func TestDeploymentOutcomesAttributeReleaseCommandExit(t *testing.T) {
	m := NewMemStore()
	now := time.Now()
	cancelAt := now
	m.mu.Lock()
	m.apps["app-1"] = App{ID: "app-1", AccountID: "acct-1"}
	for _, tc := range []struct {
		id     string
		status AppTaskStatus
		exit   *int
		stderr string
		cancel *time.Time
	}{
		{"exit1", AppTaskFailed, exitCode(1), "", nil},
		{"exit3", AppTaskFailed, exitCode(3), "", nil},
		{"own-timeout", AppTaskTimedOut, nil, "", nil},
		{"owner-cancel", AppTaskCancelled, nil, "", &cancelAt},
		{"platform-cancel", AppTaskCancelled, nil, "", nil},
		{"npm-not-found", AppTaskFailed, exitCode(127), "/bin/sh: 1: npm: not found\n", nil},
		{"customer-typo", AppTaskFailed, exitCode(127), "/bin/sh: 1: nmp: not found\n", nil},
		{"no-task", "", nil, "", nil},
	} {
		appID := "app-" + tc.id
		m.apps[appID] = App{ID: appID, AccountID: "acct-1"}
		m.putDeploymentLocked(tc.id, Deployment{ID: tc.id, AppID: appID, Status: DeployFailed, ErrorCode: api.CodeReleaseCommandFailed, CreatedAt: now})
		if tc.status != "" {
			m.appTasks["t-"+tc.id] = AppTask{ID: "t-" + tc.id, AppID: appID, DeploymentID: tc.id, Kind: AppTaskKindRelease, Status: tc.status, ExitCode: tc.exit, StderrTail: tc.stderr, CancelRequested: tc.cancel}
		}
	}
	m.mu.Unlock()

	counts, err := m.CountDeploymentOutcomesSince(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// platform-cancel, npm-not-found and no-task stay platform evidence; a
	// typo in the customer's own command is theirs.
	if counts.Failed != 3 || counts.FailedAccounts != 1 {
		t.Fatalf("outcomes = %+v, want 3 platform release failures on 1 account", counts)
	}
}

func exitCode(v int) *int { return &v }
