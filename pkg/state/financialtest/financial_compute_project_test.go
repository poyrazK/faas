package financialtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectComputeTestStore interface {
	computeTestStore
	CreateProject(context.Context, state.Project) (state.Project, error)
}

// adr: 1750 — amendment 1: one strict project cap funds every app in the
// project from a shared book, admits no more than the cap across apps,
// holds the whole project, leaves apps outside it alone, and refuses
// activation while a project app is running.
func TestFinancialStrictProjectCompute(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store projectComputeTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			strictProjectSuite(t, store)
		})
	}
}

func strictProjectSuite(t *testing.T, store projectComputeTestStore) {
	ctx := t.Context()
	a := financialAccount(t, store)
	project, err := store.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "capped", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app := func(slug, projectID string) state.App {
		t.Helper()
		created, err := store.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: projectID, WorkloadName: slug, Slug: slug, RAMMB: 256, MaxConcurrency: 20})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	first, second, outside := app("project-first", project.ID), app("project-second", project.ID), app("outside", "")
	nodes, err := store.ListComputeNodes(ctx, false)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("nodes: %v %v", nodes, err)
	}
	owner := runtimeadmission.Identity{NodeID: nodes[0].ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
	if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterFinancialComputeOwner(ctx, owner.NodeID, owner.Incarnation); err != nil {
		t.Fatal(err)
	}
	for at := project.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	// The largest funding one attempt can take, whatever second it starts in.
	start := time.Now().UTC().Truncate(time.Second)
	charge := int64(0)
	for offset := range 60 {
		at := start.Truncate(time.Minute).Add(time.Duration(offset) * time.Second)
		candidate, err := financial.ComputeCharge(264, at, at.Add(api.FinancialComputeLeaseDuration+api.FinancialComputeTeardownReserve), api.OverageMillicentsPerGBHour, api.SecondsPerGBHour)
		if err != nil {
			t.Fatal(err)
		}
		charge = max(charge, candidate)
	}
	spec := financialBudgetSpec()
	spec.Scope = financial.BudgetScope{Kind: "project", ID: project.ID}
	spec.Mode, spec.Action, spec.Basis, spec.ResumeRule = "strict", "suspend_workloads", "gross_usage", "manual"
	spec.NotifyMillicents, spec.LimitMillicents = []int64{}, charge
	if !financial.StrictProjectCompute(spec) {
		t.Fatal("spec is not a strict project cap")
	}
	p, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", spec)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	request := func(app state.App) financial.ComputeRequest {
		return financial.ComputeRequest{AttemptID: uuid.NewString(), Fenced: true, LeaseID: uuid.NewString(), Fence: uuid.NewString(), RequestID: uuid.NewString(), AccountID: a.ID, AppID: app.ID, InstanceID: uuid.NewString(), NodeID: owner.NodeID, Incarnation: owner.Incarnation, BillableMB: 264}
	}

	policies, err := store.FinancialComputePolicies(ctx, a.ID, second.ID)
	if err != nil || len(policies) != 1 || policies[0].ID != p.ID {
		t.Fatalf("project policy not reported for its app: %+v %v", policies, err)
	}
	if policies, err := store.FinancialComputePolicies(ctx, a.ID, outside.ID); err != nil || len(policies) != 0 {
		t.Fatalf("project policy reported outside the project: %+v %v", policies, err)
	}

	l, err := store.AcquireFinancialCompute(ctx, request(first))
	if err != nil || !l.Strict || l.ProjectID != project.ID || l.Reserved == 0 {
		t.Fatalf("first project app: %+v %v", l, err)
	}
	b, err := store.FinancialComputeBalance(ctx, p, time.Now())
	if err != nil || b.ProjectID != project.ID || b.ReservedMillicents != l.Reserved || !b.Initialized {
		t.Fatalf("project balance: %+v %v", b, err)
	}
	// The second app shares the book: the cap admits no second attempt.
	if denied, err := store.AcquireFinancialCompute(ctx, request(second)); !errors.Is(err, financial.ErrComputeBudget) || !denied.Denied {
		t.Fatalf("project cap oversubscribed across apps: %+v %v", denied, err)
	}
	if b, err := store.FinancialComputeBalance(ctx, p, time.Now()); err != nil || !b.Held || b.CommittedMillicents+b.ReservedMillicents > spec.LimitMillicents {
		t.Fatalf("held project balance: %+v %v", b, err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, fullStore(t, store), second, "", time.Now()); !errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("held project admitted traffic: %v", err)
	}
	// An app outside the project neither funds nor is held by the cap.
	free, err := store.AcquireFinancialCompute(ctx, request(outside))
	if err != nil || free.Strict || free.ProjectID != "" {
		t.Fatalf("outside app: %+v %v", free, err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, fullStore(t, store), outside, "", time.Now()); err != nil {
		t.Fatalf("outside app held: %v", err)
	}

	// Settlement moves the project book with the app's: nothing stays reserved.
	settled, err := store.SettleFinancialCompute(ctx, financial.ComputeRequest{LeaseID: l.ID, Fence: l.Fence, RequestID: uuid.NewString(), AccountID: l.AccountID, AppID: l.AppID, InstanceID: l.InstanceID, NodeID: l.NodeID, Incarnation: l.Incarnation, BillableMB: l.BillableMB, Revision: l.Revision})
	if err != nil || settled.SettledAt == nil || settled.Reserved != 0 {
		t.Fatalf("settle: %+v %v", settled, err)
	}
	if b, err := store.FinancialComputeBalance(ctx, p, time.Now()); err != nil || b.ReservedMillicents != 0 || b.CommittedMillicents != settled.Committed || b.UnconfirmedLeases != 0 || !b.Held {
		t.Fatalf("settled project balance (manual hold remains): %+v %v", b, err)
	}

	// Activation needs a quiet project: a running project app refuses it.
	busy, err := store.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "busy", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	running := app("busy-app", busy.ID)
	if _, err := store.AcquireFinancialCompute(ctx, request(running)); err != nil {
		t.Fatalf("unbudgeted busy app: %v", err)
	}
	busySpec := spec
	busySpec.Scope.ID = busy.ID
	if _, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", busySpec); !errors.Is(err, financial.ErrComputeUnavailable) {
		t.Fatalf("busy project activated: %v", err)
	}

	// Two apps share a larger cap: renewal and settlement keep the project
	// book equal to the sum of its attempts.
	shared, err := store.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "shared", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	left, right := app("shared-left", shared.ID), app("shared-right", shared.ID)
	sharedSpec := spec
	sharedSpec.Scope.ID, sharedSpec.LimitMillicents = shared.ID, 3*charge
	sp, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", sharedSpec)
	if err != nil {
		t.Fatalf("activate shared: %v", err)
	}
	l1, err := store.AcquireFinancialCompute(ctx, request(left))
	if err != nil {
		t.Fatal(err)
	}
	l2, err := store.AcquireFinancialCompute(ctx, request(right))
	if err != nil {
		t.Fatal(err)
	}
	mutation := func(l financial.ComputeLease) financial.ComputeRequest {
		return financial.ComputeRequest{LeaseID: l.ID, Fence: l.Fence, RequestID: uuid.NewString(), AccountID: l.AccountID, AppID: l.AppID, InstanceID: l.InstanceID, NodeID: l.NodeID, Incarnation: l.Incarnation, BillableMB: l.BillableMB, Revision: l.Revision}
	}
	renewed, err := store.RenewFinancialCompute(ctx, mutation(l1))
	if err != nil || renewed.ProjectID != shared.ID {
		t.Fatalf("renew: %+v %v", renewed, err)
	}
	if b, err := store.FinancialComputeBalance(ctx, sp, time.Now()); err != nil || b.ReservedMillicents+b.CommittedMillicents != renewed.Reserved+renewed.Committed+l2.Reserved+l2.Committed || b.UnconfirmedLeases != 2 || b.Held {
		t.Fatalf("shared project balance after renewal: %+v (attempts %d+%d, %d+%d) %v", b, renewed.Committed, renewed.Reserved, l2.Committed, l2.Reserved, err)
	}
	s1, err := store.SettleFinancialCompute(ctx, mutation(renewed))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.SettleFinancialCompute(ctx, mutation(l2))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := store.FinancialComputeBalance(ctx, sp, time.Now()); err != nil || b.ReservedMillicents != 0 || b.CommittedMillicents != s1.Committed+s2.Committed || b.UnconfirmedLeases != 0 {
		t.Fatalf("shared project balance after settlement: %+v %v", b, err)
	}
}
