package financialtest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1644 — a wake without a persisted attempt identity cannot be funded,
// but an app without a strict cap is still registered and allowed to run.
func TestFinancialComputeUnfencedDeniedOnlyWhenStrict(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	a := financialAccount(t, store)
	app, err := store.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "compute-unfenced", RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListComputeNodes(ctx, false)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("nodes: %v %v", nodes, err)
	}
	var owner runtimeadmission.Identity
	for _, n := range nodes {
		owner = runtimeadmission.Identity{NodeID: n.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
		if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterFinancialComputeOwner(ctx, owner.NodeID, owner.Incarnation); err != nil {
			t.Fatal(err)
		}
	}
	unfenced := func() financial.ComputeRequest {
		return financial.ComputeRequest{AttemptID: uuid.NewString(), AccountID: a.ID, AppID: app.ID, InstanceID: uuid.NewString(), NodeID: owner.NodeID, Incarnation: owner.Incarnation, BillableMB: 136}
	}
	l, err := store.AcquireFinancialCompute(ctx, unfenced())
	if err != nil || l.Strict || l.Denied || l.SettledAt != nil {
		t.Fatalf("unbudgeted unfenced wake was refused: %+v %v", l, err)
	}

	for at := app.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SettleFinancialCompute(ctx, financial.ComputeRequest{LeaseID: l.ID, Fence: l.Fence, RequestID: uuid.NewString(), Revision: l.Revision, AccountID: l.AccountID, AppID: l.AppID, InstanceID: l.InstanceID, NodeID: l.NodeID, Incarnation: l.Incarnation, BillableMB: l.BillableMB}); err != nil {
		t.Fatal(err)
	}
	spec := financialBudgetSpec()
	spec.Scope = financial.BudgetScope{Kind: "app", ID: app.ID}
	spec.Mode, spec.Action, spec.Basis = "strict", "suspend_workloads", "gross_usage"
	spec.NotifyMillicents = []int64{}
	spec.DrainSeconds, spec.LimitMillicents, spec.ResumeRule = 0, 100_000, "manual"
	if _, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", spec); err != nil {
		t.Fatal(err)
	}
	denied, err := store.AcquireFinancialCompute(ctx, unfenced())
	if !errors.Is(err, financial.ErrComputeBudget) || !denied.Denied || denied.DenialReason != "attempt_identity_missing" {
		t.Fatalf("strict cap funded an unfenced wake: %+v %v", denied, err)
	}
}
