package sched_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type costControlForkVMM struct {
	sched.RoutedVMM
	restores int
}

func (v *costControlForkVMM) CreateFromSnapshot(_ context.Context, _, instance string, _ sched.AppSpec, _ sched.SnapshotRef) (*sched.WakeOutcome, error) {
	v.restores++
	return &sched.WakeOutcome{Instance: instance, LeaseUID: 20001, HostIP: "10.100.0.2", Netns: "fc-" + instance, VethHost: "vh-audit", VethPeer: "vp-audit"}, nil
}
func (v *costControlForkVMM) Destroy(context.Context, string, string) error { return nil }

type costControlNotifier struct{}

func (costControlNotifier) Notify(context.Context, string, string) error { return nil }

func TestCostControlPreviewBudgetBlocksForkRestore(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "audit-held-fork@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "audit-preview-fork", PreviewOfSlug: "production", RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:audit-fork", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 256 << 20, StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "audit"), Tier: state.SnapshotTierInit}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fork, err := store.CreateAppFork(t.Context(), state.CreateAppForkParams{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, RequestedBy: "audit", TTLSeconds: 600, MaxPerApp: 1, MaxPerAccount: 2, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "stop previews", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 100, Mode: "monitored", Action: "stop_previews", ResumeRule: "manual", Enabled: true}
	policy, err := store.CreateFinancialBudget(t.Context(), account.ID, uuid.NewString(), "audit", spec)
	if err != nil {
		t.Fatal(err)
	}
	month := financial.ComputeMonth(now)
	known := int64(100)
	if err := store.RecordFinancialBudgetEvaluation(t.Context(), account.ID, policy.ID, policy.Revision, financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, KnownMillicents: &known, Fresh: true, CoverageComplete: true}); err != nil {
		t.Fatal(err)
	}
	if err := state.CheckFinancialBudgetAdmission(t.Context(), store, app, dep.ID, now); !errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("fixture did not hold the preview: %v", err)
	}
	vmm := &costControlForkVMM{}
	engine, err := sched.NewEngine(t.Context(), store, sched.NewNodeLedger(), vmm, costControlNotifier{}, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := engine.RestoreFork(t.Context(), fork)
	if !errors.Is(err, state.ErrFinancialBudgetHeld) || vmm.restores != 0 {
		t.Errorf("held preview reached fork restore: error=%v; VM restores=%d; instance=%s", err, vmm.restores, restored.InstanceID)
	}
}
