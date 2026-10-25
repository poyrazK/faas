package sched_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1750 — amendment 2: a held environment cap retires only the instances
// whose deployment runs in that environment; the app keeps serving others.
func TestStrictEnvironmentHoldRetiresOnlyItsEnvironment(t *testing.T) {
	ctx := t.Context()
	s := state.NewMemStore()
	a, err := s.CreateAccount(ctx, "environment-cap@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "environment-cap", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: project.ID, Slug: "stage-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: project.ID, Slug: "stage-b"}); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: project.ID, WorkloadName: "web", Slug: "environment-cap-web", RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(scope string) state.Deployment {
		t.Helper()
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:environment-cap", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	capped, sibling := deploy("stage-a"), deploy("stage-b")
	nodes, err := s.ListComputeNodes(ctx, false)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("nodes: %v %v", nodes, err)
	}
	owner := runtimeadmission.Identity{NodeID: nodes[0].ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterFinancialComputeOwner(ctx, owner.NodeID, owner.Incarnation); err != nil {
		t.Fatal(err)
	}
	for at := project.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := s.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	// A zero cap activates on a quiet environment and denies its first attempt.
	spec := financial.BudgetSpec{Name: "Stage compute", Scope: financial.BudgetScope{Kind: "environment", ID: stage.ID}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", NotifyMillicents: []int64{}, Enabled: true}
	p, err := s.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "editor", spec)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	e := sched.XNewEngine(t, s, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	inCap, err := e.Wake(ctx, app.ID, capped.ID, "stage-a", sched.TriggerGateway)
	if err != nil {
		t.Fatalf("wake capped: %v", err)
	}
	inSibling, err := e.Wake(ctx, app.ID, sibling.ID, "stage-b", sched.TriggerGateway)
	if err != nil {
		t.Fatalf("wake sibling: %v", err)
	}
	denied := financial.ComputeRequest{AttemptID: uuid.NewString(), Fenced: true, LeaseID: uuid.NewString(), Fence: uuid.NewString(), RequestID: uuid.NewString(), AccountID: a.ID, AppID: app.ID, InstanceID: uuid.NewString(), DeploymentID: capped.ID, NodeID: owner.NodeID, Incarnation: owner.Incarnation, BillableMB: 136}
	if _, err := s.AcquireFinancialCompute(ctx, denied); !errors.Is(err, financial.ErrComputeBudget) {
		t.Fatalf("zero cap funded an attempt: %v", err)
	}
	if b, err := s.FinancialComputeBalance(ctx, p, time.Now()); err != nil || !b.Held {
		t.Fatalf("environment not held: %+v %v", b, err)
	}
	if err := e.ReconcileFinancialBudgetStops(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	for id, running := range map[string]bool{inCap.InstanceID: false, inSibling.InstanceID: true} {
		ins, err := s.InstanceByID(ctx, id)
		if err != nil || state.State(ins.State).CountsForRAM() != running {
			t.Fatalf("instance %s resident=%t, want %t: %v", id, state.State(ins.State).CountsForRAM(), running, err)
		}
	}
}
