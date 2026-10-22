package apidserver

import (
	"context"
	"testing"
	"time"
)

func TestEnvironmentGitOpsAcceptanceEnforcementClosesGateWhenWorkerExits(t *testing.T) {
	srv, _, _, _, _ := newProjectLifecycleFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	stop := srv.startEnvironmentGitAcceptanceEnforcement(ctx, func(name string) string {
		if name == environmentGitOpsAcceptanceEnforcementFlag || name == "GREGALE_GITOPS_ACCEPTANCE" || name == "FAAS_DEV" {
			return "1"
		}
		return ""
	})
	defer stop()
	if !srv.environmentGitOpsAcceptanceEnforcement.Load() {
		t.Fatal("acceptance worker did not open its matching API gate")
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for srv.environmentGitOpsAcceptanceEnforcement.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if srv.environmentGitOpsAcceptanceEnforcement.Load() {
		t.Fatal("exited acceptance worker left customer enforcement open")
	}
}

func TestEnvironmentGitOpsAcceptanceEnforcementRunsRealBackendWithoutClaimingServing(t *testing.T) {
	notifier := &gitOpsFleetNotifier{nodes: []string{"node-a"}}
	srv, store, source, apps := gitOpsBackendFixture(t, 1, notifier)
	stop := srv.startEnvironmentGitAcceptanceEnforcement(t.Context(), func(name string) string {
		if name == environmentGitOpsAcceptanceEnforcementFlag || name == "GREGALE_GITOPS_ACCEPTANCE" || name == "FAAS_DEV" {
			return "1"
		}
		return ""
	})
	t.Cleanup(stop)
	if !srv.environmentGitOpsAcceptanceEnforcement.Load() {
		t.Fatal("isolated acceptance worker did not open its matching API gate")
	}
	run := waitGitOpsReport(t, store, source, "")
	if run.Status != "partial" || run.RevisionID != source.ApprovedRevisionID {
		t.Fatalf("acceptance worker claimed serving before native qualification: %+v", run)
	}
	values, err := store.ListAppEnvInScope(t.Context(), source.AccountID, apps[0].ID, "production")
	if err != nil || len(values) != 1 || values[0].Value != "production" {
		t.Fatalf("acceptance executor did not restore reviewed intent: %+v %v", values, err)
	}
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if err != nil || current.AppliedRevisionID != "" {
		t.Fatalf("intent-only progress published applied revision: %+v %v", current, err)
	}
}
