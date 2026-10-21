package apidserver

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

func acceptanceEnforcementEnv(name string) string {
	if name == environmentGitOpsAcceptanceEnforcementFlag || name == "GREGALE_GITOPS_ACCEPTANCE" || name == "FAAS_DEV" {
		return "1"
	}
	return ""
}

func TestEnvironmentGitOpsEnforcementClosesGateWhenWorkerExits(t *testing.T) {
	srv, _, account, _, _ := newProjectLifecycleFixture(t)
	srv.seedEnvPlatformFlags(acceptanceEnforcementEnv)
	ctx, cancel := context.WithCancel(t.Context())
	stop := srv.startEnvironmentGitEnforcement(ctx)
	defer stop()
	if !srv.environmentGitEnforcementAvailable(account) {
		t.Fatal("enforce worker did not open its matching API gate")
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for srv.environmentGitEnforcementAvailable(account) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if srv.environmentGitEnforcementAvailable(account) {
		t.Fatal("exited enforce worker left customer enforcement open")
	}
}

func TestEnvironmentGitOpsEnforcementRunsRealBackendWithoutClaimingServing(t *testing.T) {
	notifier := &gitOpsFleetNotifier{nodes: []string{"node-a"}}
	srv, store, source, apps := gitOpsBackendFixture(t, 1, notifier)
	srv.seedEnvPlatformFlags(acceptanceEnforcementEnv)
	t.Cleanup(srv.startEnvironmentGitEnforcement(t.Context()))
	run := waitGitOpsReport(t, store, source, "")
	if run.Status != "partial" || run.RevisionID != source.ApprovedRevisionID {
		t.Fatalf("enforce worker claimed serving before native qualification: %+v", run)
	}
	values, err := store.ListAppEnvInScope(t.Context(), source.AccountID, apps[0].ID, "production")
	if err != nil || len(values) != 1 || values[0].Value != "production" {
		t.Fatalf("enforce executor did not restore reviewed intent: %+v %v", values, err)
	}
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if err != nil || current.AppliedRevisionID != "" {
		t.Fatalf("intent-only progress published applied revision: %+v %v", current, err)
	}
}

// A production control plane starts with GitOps execution off: the enforce
// worker holds an enforced source without touching its intent.
func TestEnvironmentGitOpsEnforcementHoldsUnadmittedAccount(t *testing.T) {
	notifier := &gitOpsFleetNotifier{nodes: []string{"node-a"}}
	srv, store, source, apps := gitOpsBackendFixture(t, 1, notifier)
	srv.seedEnvPlatformFlags(func(string) string { return "" })
	t.Cleanup(srv.startEnvironmentGitEnforcement(t.Context()))
	run := waitGitOpsReport(t, store, source, "")
	if run.Status != "blocked" || run.ErrorCode != environmentgitops.EnforcementUnavailableCode {
		t.Fatalf("unadmitted account was executed: %+v", run)
	}
	values, err := store.ListAppEnvInScope(t.Context(), source.AccountID, apps[0].ID, "production")
	if err != nil || len(values) != 1 || values[0].Value != "console" {
		t.Fatalf("held source changed intent: %+v %v", values, err)
	}
	value := true
	srv.seedPlatformFlag(platformflags.EnvironmentGitOpsExecution, platformflags.Definition{Enabled: true,
		Rules: []platformflags.Rule{{ID: "accounts", Accounts: []string{source.AccountID}, Value: &value}}})
	if !srv.environmentGitEnforcementAdmitted(t.Context(), source.AccountID) {
		t.Fatal("an account rule did not admit the source's account")
	}
	srv.seedPlatformFlag(platformflags.EnvironmentGitOpsExecution, platformflags.Definition{Default: true})
	if srv.environmentGitEnforcementAdmitted(t.Context(), source.AccountID) {
		t.Fatal("the kill switch did not hold every account")
	}
}
