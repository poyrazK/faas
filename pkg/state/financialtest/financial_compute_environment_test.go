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

type environmentComputeTestStore interface {
	projectComputeTestStore
	CreateProjectEnvironment(context.Context, state.ProjectEnvironment) (state.ProjectEnvironment, error)
	CreateInstance(ctx context.Context, appID, deploymentID, state string, ramMB int, nodeID, wakeID string) (state.Instance, error)
}

// adr: 1750 — amendment 2: one strict environment cap funds every deployment
// in the environment from a shared book across apps, holds only that
// environment, fails closed without a deployment, and refuses activation
// while a deployment in the environment is running.
func TestFinancialStrictEnvironmentCompute(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store environmentComputeTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			strictEnvironmentSuite(t, store)
		})
	}
}

// strictScopeOwner registers a capable native owner on the first node.
func strictScopeOwner(t *testing.T, store computeTestStore) runtimeadmission.Identity {
	t.Helper()
	ctx := t.Context()
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
	return owner
}

// strictScopeSampling records complete sampling windows since the scope began.
func strictScopeSampling(t *testing.T, store computeTestStore, since time.Time) {
	t.Helper()
	for at := since.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(t.Context(), at, true, false); err != nil {
			t.Fatal(err)
		}
	}
}

// strictScopeCharge is the largest funding one attempt can take, whatever
// second it starts in.
func strictScopeCharge(t *testing.T) int64 {
	t.Helper()
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
	return charge
}

func strictEnvironmentSuite(t *testing.T, store environmentComputeTestStore) {
	ctx := t.Context()
	a := financialAccount(t, store)
	project, err := store.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "environments", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	env := func(slug string) state.ProjectEnvironment {
		t.Helper()
		created, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: project.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	stage, sibling, shared := env("stage-a"), env("stage-b"), env("stage-c")
	app := func(slug string) state.App {
		t.Helper()
		created, err := store.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: project.ID, WorkloadName: slug, Slug: slug, RAMMB: 256, MaxConcurrency: 20})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	left, right := app("env-left"), app("env-right")
	deploy := func(app state.App, scope string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:environment-cap"})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	leftStage, rightStage, leftSibling := deploy(left, "stage-a"), deploy(right, "stage-a"), deploy(left, "stage-b")
	owner := strictScopeOwner(t, store)
	strictScopeSampling(t, store, project.CreatedAt)
	charge := strictScopeCharge(t)
	spec := financialBudgetSpec()
	spec.Scope = financial.BudgetScope{Kind: "environment", ID: stage.ID}
	spec.Mode, spec.Action, spec.Basis, spec.ResumeRule = "strict", "suspend_workloads", "gross_usage", "manual"
	spec.NotifyMillicents, spec.LimitMillicents = []int64{}, charge
	if !financial.StrictEnvironmentCompute(spec) || !financial.StrictCompute(spec) {
		t.Fatal("spec is not a strict environment cap")
	}
	p, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", spec)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	request := func(app state.App, deployment string) financial.ComputeRequest {
		return financial.ComputeRequest{AttemptID: uuid.NewString(), Fenced: true, LeaseID: uuid.NewString(), Fence: uuid.NewString(), RequestID: uuid.NewString(), AccountID: a.ID, AppID: app.ID, InstanceID: uuid.NewString(), DeploymentID: deployment, NodeID: owner.NodeID, Incarnation: owner.Incarnation, BillableMB: 264}
	}

	// Every app of the project reports the cap; a deployment narrows it.
	if policies, err := store.FinancialComputePolicies(ctx, a.ID, right.ID); err != nil || len(policies) != 1 || policies[0].ID != p.ID {
		t.Fatalf("environment policy not reported for a project app: %+v %v", policies, err)
	}
	for deployment, want := range map[string]string{leftStage.ID: stage.ID, leftSibling.ID: sibling.ID} {
		if got, err := store.FinancialComputeDeploymentEnvironment(ctx, a.ID, left.ID, deployment); err != nil || got != want {
			t.Fatalf("deployment %s environment = %q, %v; want %q", deployment, got, err, want)
		}
	}
	if _, err := store.FinancialComputeDeploymentEnvironment(ctx, a.ID, right.ID, leftStage.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another app's deployment resolved: %v", err)
	}

	l, err := store.AcquireFinancialCompute(ctx, request(left, leftStage.ID))
	if err != nil || !l.Strict || l.EnvironmentID != stage.ID || l.DeploymentID != leftStage.ID || l.ProjectID != "" || l.Reserved == 0 {
		t.Fatalf("first environment attempt: %+v %v", l, err)
	}
	b, err := store.FinancialComputeBalance(ctx, p, time.Now())
	if err != nil || b.EnvironmentID != stage.ID || b.ReservedMillicents != l.Reserved || !b.Initialized {
		t.Fatalf("environment balance: %+v %v", b, err)
	}
	// Another app's deployment in the environment shares the book.
	if denied, err := store.AcquireFinancialCompute(ctx, request(right, rightStage.ID)); !errors.Is(err, financial.ErrComputeBudget) || !denied.Denied {
		t.Fatalf("environment cap oversubscribed across apps: %+v %v", denied, err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, fullStore(t, store), right, rightStage.ID, time.Now()); !errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("held environment admitted traffic: %v", err)
	}
	// The hold stops only the environment: the same app's other deployment,
	// and calls that name no deployment, are admitted.
	if err := state.CheckFinancialBudgetAdmission(ctx, fullStore(t, store), left, leftSibling.ID, time.Now()); err != nil {
		t.Fatalf("sibling environment held: %v", err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, fullStore(t, store), left, "", time.Now()); err != nil {
		t.Fatalf("call without a deployment held: %v", err)
	}
	free, err := store.AcquireFinancialCompute(ctx, request(left, leftSibling.ID))
	if err != nil || free.Strict || free.EnvironmentID != "" || free.DeploymentID != leftSibling.ID {
		t.Fatalf("sibling deployment: %+v %v", free, err)
	}
	// Without a deployment the environment is unknown: fail closed.
	if _, err := store.AcquireFinancialCompute(ctx, request(right, "")); !errors.Is(err, financial.ErrComputeUnavailable) {
		t.Fatalf("attempt without a deployment admitted: %v", err)
	}
	// The meter bills a capped environment's residency through funding: an
	// unfunded VM there bills nothing, one in the sibling environment bills.
	if managed, err := store.FinancialComputeAppManaged(ctx, a.ID, right.ID); err != nil || !managed {
		t.Fatalf("environment app not managed: %t %v", managed, err)
	}
	minute := time.Now().UTC().Truncate(time.Minute)
	// An environment cap opened the apps' own books, which still waive no
	// capacity floor: a floor has no environment.
	for _, app := range []state.App{left, right} {
		if covered, err := store.FinancialComputeFloorCovered(ctx, a.ID, app.ID, minute); err != nil || covered {
			t.Fatalf("environment cap waived %s's floor: %t %v", app.Slug, covered, err)
		}
	}
	for _, c := range []struct {
		app        state.App
		deployment string
		want       int64
	}{{right, rightStage.ID, 0}, {left, leftSibling.ID, 60}} {
		ins, err := store.CreateInstance(ctx, c.app.ID, c.deployment, "running", 256, owner.NodeID, "")
		if err != nil {
			t.Fatal(err)
		}
		if seconds, err := store.FinancialComputeBillingSeconds(ctx, a.ID, c.app.ID, ins.ID, minute, 60); err != nil || seconds != c.want {
			t.Fatalf("unfunded residency in %s billed %d, %v; want %d", c.deployment, seconds, err, c.want)
		}
	}

	settled, err := store.SettleFinancialCompute(ctx, computeMutation(l))
	if err != nil || settled.SettledAt == nil || settled.Reserved != 0 {
		t.Fatalf("settle: %+v %v", settled, err)
	}
	if b, err := store.FinancialComputeBalance(ctx, p, time.Now()); err != nil || b.ReservedMillicents != 0 || b.CommittedMillicents != settled.Committed || b.UnconfirmedLeases != 0 || !b.Held {
		t.Fatalf("settled environment balance (manual hold remains): %+v %v", b, err)
	}

	// Activation needs a quiet environment: the sibling deployment runs.
	siblingSpec := spec
	siblingSpec.Scope.ID = sibling.ID
	if _, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", siblingSpec); !errors.Is(err, financial.ErrComputeUnavailable) {
		t.Fatalf("busy environment activated: %v", err)
	}
	if _, err := store.SettleFinancialCompute(ctx, computeMutation(free)); err != nil {
		t.Fatal(err)
	}

	// Two apps share a larger cap: renewal and settlement keep the book equal
	// to the sum of its attempts.
	sharedSpec := spec
	sharedSpec.Scope.ID, sharedSpec.LimitMillicents = shared.ID, 3*charge
	sp, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", sharedSpec)
	if err != nil {
		t.Fatalf("activate shared: %v", err)
	}
	l1, err := store.AcquireFinancialCompute(ctx, request(left, deploy(left, "stage-c").ID))
	if err != nil {
		t.Fatal(err)
	}
	l2, err := store.AcquireFinancialCompute(ctx, request(right, deploy(right, "stage-c").ID))
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := store.RenewFinancialCompute(ctx, computeMutation(l1))
	if err != nil || renewed.EnvironmentID != shared.ID {
		t.Fatalf("renew: %+v %v", renewed, err)
	}
	if b, err := store.FinancialComputeBalance(ctx, sp, time.Now()); err != nil || b.ReservedMillicents+b.CommittedMillicents != renewed.Reserved+renewed.Committed+l2.Reserved+l2.Committed || b.UnconfirmedLeases != 2 || b.Held {
		t.Fatalf("shared environment balance after renewal: %+v %v", b, err)
	}
	s1, err := store.SettleFinancialCompute(ctx, computeMutation(renewed))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.SettleFinancialCompute(ctx, computeMutation(l2))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := store.FinancialComputeBalance(ctx, sp, time.Now()); err != nil || b.ReservedMillicents != 0 || b.CommittedMillicents != s1.Committed+s2.Committed || b.UnconfirmedLeases != 0 {
		t.Fatalf("shared environment balance after settlement: %+v %v", b, err)
	}
}

func computeMutation(l financial.ComputeLease) financial.ComputeRequest {
	return financial.ComputeRequest{LeaseID: l.ID, Fence: l.Fence, RequestID: uuid.NewString(), AccountID: l.AccountID, AppID: l.AppID, InstanceID: l.InstanceID, NodeID: l.NodeID, Incarnation: l.Incarnation, BillableMB: l.BillableMB, Revision: l.Revision}
}
