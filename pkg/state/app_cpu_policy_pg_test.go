//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAppCPUPolicyReconcilesAndRecordsPerNodeApply(t *testing.T) {
	store, ctx := pgStore(t)
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "cpu-policy-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "cpu-policy-" + suffix,
		Type: state.AppTypeApp, Status: state.AppActive,
		RAMMB: 256, CPUMillicores: 1000, MaxConcurrency: 2,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:cpu-policy-test",
		Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	role := "compute-node"
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "cpu-policy-" + suffix, TargetURL: "tcp://127.0.0.1:50051",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512,
		VCPUBudget: 1, Active: true, Role: &role,
	})
	if err != nil {
		t.Fatalf("create compute node: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 256, node.ID, ""); err != nil {
		t.Fatalf("create live instance: %v", err)
	}

	loadPending := func() []state.AppCPUPolicyApplyTarget {
		t.Helper()
		targets, err := store.ListPendingAppCPUPolicyTargets(ctx, app.ID, time.Minute, 20)
		if err != nil {
			t.Fatalf("list pending targets: %v", err)
		}
		return targets
	}
	if desired, err := store.LatestAppCPUPolicyRevision(ctx, app.ID); err != nil || desired != 1 {
		t.Fatalf("initial desired revision = %d, %v; want 1", desired, err)
	}
	targets := loadPending()
	if len(targets) != 1 || targets[0].NodeID != node.ID || targets[0].Revision != 1 || targets[0].CPUMillicores != 1000 {
		t.Fatalf("initial targets = %+v; want 1000m policy revision 1 on one node", targets)
	}
	if err := store.RecordAppCPUPolicyApply(ctx, app.ID, node.ID, 1, errors.New("vmmd unavailable")); err != nil {
		t.Fatalf("record failed apply: %v", err)
	}
	if len(loadPending()) != 1 {
		t.Fatal("failed apply was not left pending for retry")
	}

	updatedCPU := 500
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{CPUMillicores: &updatedCPU}); err != nil {
		t.Fatalf("update CPU quota: %v", err)
	}
	if desired, err := store.LatestAppCPUPolicyRevision(ctx, app.ID); err != nil || desired != 2 {
		t.Fatalf("updated desired revision = %d, %v; want 2", desired, err)
	}
	targets = loadPending()
	if len(targets) != 1 || targets[0].Revision != 2 || targets[0].CPUMillicores != 500 {
		t.Fatalf("updated targets = %+v; want 500m revision 2", targets)
	}
	if err := store.RecordAppCPUPolicyApply(ctx, app.ID, node.ID, 2, nil); err != nil {
		t.Fatalf("record successful apply: %v", err)
	}
	if targets = loadPending(); len(targets) != 0 {
		t.Fatalf("targets after successful apply = %+v, want none", targets)
	}
	nodes, err := store.ListServingAppCPUPolicyNodeStates(ctx, app.ID)
	if err != nil {
		t.Fatalf("list serving node status: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeName != node.Name || nodes[0].DesiredRevision != 2 || nodes[0].AppliedRevision != 2 || nodes[0].LastError != "" {
		t.Fatalf("serving node status = %+v, want node applied at revision 2", nodes)
	}

	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{MaxConcurrency: cpuPolicyIntPtr(3)}); err != nil {
		t.Fatalf("update unrelated setting: %v", err)
	}
	if desired, err := store.LatestAppCPUPolicyRevision(ctx, app.ID); err != nil || desired != 2 {
		t.Fatalf("revision after unrelated setting = %d, %v; want 2", desired, err)
	}
}

func cpuPolicyIntPtr(value int) *int { return &value }
