//go:build !no_pg

package state_test

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAppEgressPolicyReconcilesAndRecordsPerNodeApply(t *testing.T) {
	store, ctx := pgStore(t)
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "egress-policy-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "egress-policy-" + suffix,
		Type: state.AppTypeApp, Status: state.AppActive,
		RAMMB: 256, MaxConcurrency: 2,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:egress-policy-test",
		Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	role := "compute-node"
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "egress-policy-" + suffix, TargetURL: "tcp://127.0.0.1:50051",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512,
		VCPUBudget: 1, Active: true, Role: &role,
	})
	if err != nil {
		t.Fatalf("create compute node: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 256, node.ID, ""); err != nil {
		t.Fatalf("create live instance: %v", err)
	}

	desired, err := store.LatestAppEgressPolicyRevision(ctx, app.ID)
	if err != nil || desired != 1 {
		t.Fatalf("initial desired revision = %d, %v; want 1", desired, err)
	}
	loadPending := func() []state.AppEgressPolicyApplyTarget {
		t.Helper()
		targets, err := store.ListPendingAppEgressPolicyTargets(ctx, app.ID, time.Minute, 20)
		if err != nil {
			t.Fatalf("list pending targets: %v", err)
		}
		return targets
	}
	targets := loadPending()
	if len(targets) != 1 || targets[0].NodeID != node.ID || targets[0].Revision != 1 || len(targets[0].Allowlist) != 0 {
		t.Fatalf("initial targets = %+v; want empty policy revision 1 on one live node", targets)
	}
	if err := store.RecordAppEgressPolicyApply(ctx, app.ID, node.ID, 1, errors.New("vmmd unavailable")); err != nil {
		t.Fatalf("record failed apply: %v", err)
	}
	if len(loadPending()) != 1 {
		t.Fatal("failed apply was not left pending for retry")
	}

	allowlist := []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		EgressAllowlist: &allowlist, SetEgressAllowlist: true,
	}); err != nil {
		t.Fatalf("update allowlist: %v", err)
	}
	desired, err = store.LatestAppEgressPolicyRevision(ctx, app.ID)
	if err != nil || desired != 2 {
		t.Fatalf("updated desired revision = %d, %v; want 2", desired, err)
	}
	targets = loadPending()
	if len(targets) != 1 || targets[0].Revision != 2 || len(targets[0].Allowlist) != 1 || targets[0].Allowlist[0] != allowlist[0] {
		t.Fatalf("updated targets = %+v; want revision 2 and %v", targets, allowlist)
	}
	if err := store.RecordAppEgressPolicyApply(ctx, app.ID, node.ID, 2, nil); err != nil {
		t.Fatalf("record successful apply: %v", err)
	}
	if targets = loadPending(); len(targets) != 0 {
		t.Fatalf("targets after successful apply = %+v, want none", targets)
	}
	nodes, err := store.ListServingAppEgressPolicyNodeStates(ctx, app.ID)
	if err != nil {
		t.Fatalf("list serving node status: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeName != node.Name || nodes[0].DesiredRevision != 2 || nodes[0].AppliedRevision != 2 || nodes[0].LastError != "" {
		t.Fatalf("serving node status = %+v, want node applied at revision 2", nodes)
	}

	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{MaxConcurrency: egressIntPtr(3)}); err != nil {
		t.Fatalf("update unrelated setting: %v", err)
	}
	if desired, err := store.LatestAppEgressPolicyRevision(ctx, app.ID); err != nil || desired != 2 {
		t.Fatalf("revision after unrelated setting = %d, %v; want 2", desired, err)
	}
}

func egressIntPtr(value int) *int { return &value }
