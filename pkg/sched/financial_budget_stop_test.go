package sched

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedBudgetPreview(t *testing.T, s *state.MemStore) (state.Account, state.App, state.Deployment) {
	t.Helper()
	a, err := s.CreateAccount(t.Context(), "preview-budget@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "arbitrary-name", PreviewOfSlug: "production", RAMMB: 128, MaxConcurrency: 5, MinInstances: 2})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	return a, app, d
}

func crossPreviewBudget(t *testing.T, s *state.MemStore, account string, now time.Time, drain int) state.FinancialBudget {
	t.Helper()
	spec := financial.BudgetSpec{Name: "Preview compute", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 100, Mode: "monitored", Action: "stop_previews", ResumeRule: "manual", DrainSeconds: drain, Enabled: true}
	p, err := s.CreateFinancialBudget(t.Context(), account, uuid.NewString(), "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	month, amount := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), int64(100)
	if err := s.RecordFinancialBudgetEvaluation(t.Context(), account, p.ID, p.Revision, financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, KnownMillicents: &amount, Fresh: true, CoverageComplete: true}); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFinancialBudgetStopDrainFailureRestartAndResume(t *testing.T) {
	s := state.NewMemStore()
	a, app, dep := seedBudgetPreview(t, s)
	vmm := &fakeVMM{}
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	now := time.Now().UTC()
	e.now = func() time.Time { return now }
	woke, err := e.Wake(t.Context(), app.ID, "", "", TriggerGateway)
	if err != nil || woke.InstanceID == "" {
		t.Fatalf("initial wake: %+v %v", woke, err)
	}
	p := crossPreviewBudget(t, s, a.ID, now, 30)
	assertTarget := func(want string, resident int) {
		t.Helper()
		stops, err := s.ActiveFinancialBudgetStops(t.Context(), a.ID, now)
		if err != nil || len(stops) != 1 {
			t.Fatalf("decisions: %+v %v", stops, err)
		}
		targets, err := s.FinancialBudgetStopTargets(t.Context(), a.ID, stops[0].ID)
		if err != nil || len(targets) != 1 || targets[0].State != want || targets[0].ResidentCount != resident {
			t.Fatalf("target acknowledgement: %+v %v", targets, err)
		}
	}
	if err := e.ReconcileFinancialBudgetStops(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	assertTarget("draining", 1)
	for _, trigger := range []string{TriggerGateway, TriggerFloorDep, TriggerDeploymentSmoke} {
		if _, err := e.AdmitInstanceForDeployment(t.Context(), app.ID, dep.ID, "", trigger); !errors.Is(err, state.ErrFinancialBudgetHeld) {
			t.Fatalf("admission bypass %s: %v", trigger, err)
		}
	}
	if _, err := e.EnsureWake(t.Context(), app.ID, TriggerGateway); !errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("warm reuse bypassed budget: %v", err)
	}
	now = now.Add(30 * time.Second)
	vmm.destroyErr = errors.New("vmmd unavailable")
	if err := e.ReconcileFinancialBudgetStops(t.Context(), app.ID); err == nil {
		t.Fatal("failed teardown was acknowledged")
	}
	assertTarget("failed", 1)
	if !e.Ledger().ResidentFor(woke.InstanceID) {
		t.Fatal("unconfirmed teardown released RAM reservation")
	}
	// A fresh scheduler reconstructs capacity and retries the durable decision.
	vmm.destroyErr = nil
	restarted := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	restarted.now = func() time.Time { return now }
	if err := restarted.ReconcileFinancialBudgetStops(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	assertTarget("enforced", 0)
	ins, err := s.InstanceByID(t.Context(), woke.InstanceID)
	if err != nil || state.State(ins.State).CountsForRAM() || restarted.Ledger().ResidentFor(ins.ID) {
		t.Fatalf("resources remain: %+v %v", ins, err)
	}
	if _, err := restarted.Wake(t.Context(), app.ID, "", "", TriggerGateway); !errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("cold wake bypassed budget: %v", err)
	}
	// Budget release must not release a different lifecycle reason.
	if _, err := s.SetAccountAbuseHold(t.Context(), a.ID, state.AccountAbuseHoldOperator, now); err != nil {
		t.Fatal(err)
	}
	p.Spec.Enabled = false
	if _, err := s.UpdateFinancialBudget(t.Context(), a.ID, p.ID, p.Revision, "editor", p.Spec); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Wake(t.Context(), app.ID, "", "", TriggerGateway); err == nil || errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("budget release cleared abuse hold: %v", err)
	}
	if _, err := s.ReleaseAccountAbuseHold(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if resumed, err := restarted.Wake(t.Context(), app.ID, "", "", TriggerGateway); err != nil || resumed.InstanceID == "" {
		t.Fatalf("explicit resume: %+v %v", resumed, err)
	}
}

func TestFinancialBudgetStopDuringBootCannotPublish(t *testing.T) {
	s := state.NewMemStore()
	a, app, _ := seedBudgetPreview(t, s)
	now := time.Now().UTC()
	vmm := &fakeVMM{coldBootHook: func() { crossPreviewBudget(t, s, a.ID, now, 0) }}
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	e.now = func() time.Time { return now }
	if out, err := e.Wake(t.Context(), app.ID, "", "", TriggerGateway); err == nil || out.InstanceID != "" {
		t.Fatalf("in-flight boot escaped hold: %+v %v", out, err)
	}
	rows, err := s.ListInstancesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if state.State(row.State).CountsForRAM() || e.Ledger().ResidentFor(row.ID) {
			t.Fatalf("rejected boot leaked capacity: %+v", row)
		}
	}
}

func TestFinancialBudgetPreviewCommandRecovery(t *testing.T) {
	s := state.NewMemStore()
	a, app, dep := seedBudgetPreview(t, s)
	ctx := t.Context()
	if err := s.SetDeploymentRootfs(ctx, dep.ID, "/tmp/preview-command.ext4", "preview/command.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := s.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: a.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, Command: []string{"true"}, TimeoutSeconds: 60, MaxOutputBytes: 1024, CreatedAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNextAppTask(ctx, "owner", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterFinancialPreviewCommand(ctx, state.FinancialPreviewCommandRuntime{TaskID: task.ID, LeaseToken: *task.LeaseToken, AccountID: a.ID, AppID: app.ID, DeploymentID: dep.ID, NodeID: node.ID, RAMMB: 128, CPUMillicores: 1000, RecordedAt: now, PrepareUntil: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	crossPreviewBudget(t, s, a.ID, now, 0)
	vmm := &fakeVMM{}
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	e.now = func() time.Time { return now }
	if err := e.SeedLedger(ctx); err != nil || !e.Ledger().ResidentFor(task.ID) {
		t.Fatalf("command capacity was lost at scheduler restart: %v", err)
	}
	if err := e.ReconcileFinancialBudgetStops(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if !e.Ledger().ResidentFor(task.ID) {
		t.Fatal("in-flight preparation was treated as confirmed teardown")
	}
	stops, err := s.ActiveFinancialBudgetStops(ctx, a.ID, now)
	if err != nil || len(stops) != 1 {
		t.Fatalf("stops: %+v %v", stops, err)
	}
	readTarget := func(want string, count int) {
		t.Helper()
		targets, err := s.FinancialBudgetStopTargets(ctx, a.ID, stops[0].ID)
		if err != nil || len(targets) != 1 || targets[0].State != want || targets[0].ResidentCount != count {
			t.Fatalf("command acknowledgement: %+v %v", targets, err)
		}
	}
	readTarget("failed", 1)
	if err := s.ActivateFinancialPreviewCommand(ctx, task.ID, *task.LeaseToken); err != nil {
		t.Fatal(err)
	}
	vmm.destroyErr = errors.New("vmmd unavailable")
	if err := e.ReconcileFinancialBudgetStops(ctx, app.ID); err == nil {
		t.Fatal("command destroy failure was hidden")
	}
	readTarget("failed", 1)
	vmm.destroyErr = nil
	if err := e.ReconcileFinancialBudgetStops(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	readTarget("enforced", 0)
	if e.Ledger().ResidentFor(task.ID) {
		t.Fatal("confirmed command teardown retained capacity")
	}
}

func TestFinancialBudgetStopDuringBootKeepsUnconfirmedCapacity(t *testing.T) {
	s := state.NewMemStore()
	a, app, _ := seedBudgetPreview(t, s)
	now := time.Now().UTC()
	vmm := &fakeVMM{destroyErr: errors.New("vmmd unavailable"), coldBootHook: func() { crossPreviewBudget(t, s, a.ID, now, 0) }}
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	e.now = func() time.Time { return now }
	if out, err := e.Wake(t.Context(), app.ID, "", "", TriggerGateway); err == nil || out.InstanceID != "" {
		t.Fatalf("budget-rejected boot was published: %+v %v", out, err)
	}
	rows, err := s.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(rows) != 1 || !state.State(rows[0].State).CountsForRAM() || !e.Ledger().ResidentFor(rows[0].ID) {
		t.Fatalf("unconfirmed boot teardown released capacity: %+v %v", rows, err)
	}
	vmm.destroyErr = nil
	if err := e.ReconcileFinancialBudgetStops(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	row, err := s.InstanceByID(t.Context(), rows[0].ID)
	if err != nil || state.State(row.State).CountsForRAM() || e.Ledger().ResidentFor(row.ID) {
		t.Fatalf("teardown retry did not settle capacity: %+v %v", row, err)
	}
}
