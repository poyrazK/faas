package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 566 — saving a strict policy requires execution authority, and an edit
// cannot invalidate funding that the API has already reported as reserved.
func TestFinancialComputeActivationAndBalances(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	store := *e.Store()
	account := (*e.Acct()).ID
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account, Slug: "strict-compute-api", RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "Strict app compute", Scope: financial.BudgetScope{Kind: "app", ID: app.ID}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 1000, NotifyMillicents: []int64{}, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true}
	rec := e.Do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": "strict-owner-unavailable"})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "financial_compute_not_ready") {
		t.Fatalf("unsupported owner activated: %d %s", rec.Code, rec.Body)
	}
	nodes, err := store.ListComputeNodes(t.Context(), false)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("nodes: %v %v", nodes, err)
	}
	var owner runtimeadmission.Identity
	for _, node := range nodes {
		owner = runtimeadmission.Identity{NodeID: node.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
		if err := store.RegisterComputeNodeRuntimeIdentity(t.Context(), owner); err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterFinancialComputeOwner(t.Context(), owner.NodeID, owner.Incarnation); err != nil {
			t.Fatal(err)
		}
	}
	p := decodeFinancialBudget(t, e.Do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": "strict-owner-ready"}), http.StatusCreated)
	if p.Status != "active" || !p.EnforcementReady || p.NotificationReady || p.Runtime == nil || p.Runtime.Compute == nil || !p.Runtime.Compute.Initialized {
		t.Fatalf("strict readiness: %+v", p)
	}
	lease, err := store.AcquireFinancialCompute(t.Context(), financial.ComputeRequest{AttemptID: uuid.NewString(), Fenced: true, LeaseID: uuid.NewString(), Fence: uuid.NewString(), RequestID: uuid.NewString(), AccountID: account, AppID: app.ID, InstanceID: uuid.NewString(), NodeID: owner.NodeID, Incarnation: owner.Incarnation, BillableMB: 136})
	if err != nil {
		t.Fatal(err)
	}
	read := decodeFinancialBudget(t, e.Do(t, "GET", "/v1/billing/budgets/"+p.ID, nil, nil), http.StatusOK)
	if read.Runtime.Compute.ReservedMillicents != lease.Reserved || read.Runtime.Compute.UnconfirmedLeases != 1 || read.Runtime.Compute.RemainingMillicents != spec.LimitMillicents-lease.Reserved {
		t.Fatalf("funding response: %+v", read.Runtime.Compute)
	}
	spec.LimitMillicents = 0
	rec = e.Do(t, "PUT", "/v1/billing/budgets/"+p.ID, api.UpdateFinancialBudgetRequest{ExpectedRevision: p.Revision, Spec: spec}, map[string]string{"Idempotency-Key": "strict-below-funding"})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "financial_compute_budget_too_low") {
		t.Fatalf("funded limit overwritten: %d %s", rec.Code, rec.Body)
	}
}
