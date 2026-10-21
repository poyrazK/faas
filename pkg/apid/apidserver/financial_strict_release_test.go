package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

// releaseStrictBudgets releases strict budgets to every account of a test
// server (ADR-1750 gates them behind the strict_budgets platform flag).
func releaseStrictBudgets(e apidserver.TestEnv) {
	(*e.S()).SeedPlatformFlag(platformflags.StrictBudgets, platformflags.Definition{Enabled: true, Default: true})
}

// adr: 1750 — strict budgets stay unreleased until the strict_budgets flag
// includes the account; withdrawing it never strands a budget already enabled.
func TestStrictBudgetReleaseGate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	store := *e.Store()
	account := (*e.Acct()).ID
	strictFor := func(slug string) financial.BudgetSpec {
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account, Slug: slug, RAMMB: 128, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		return financial.BudgetSpec{Name: "Strict " + slug, Scope: financial.BudgetScope{Kind: "app", ID: app.ID}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 1000, NotifyMillicents: []int64{}, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true}
	}
	unreleased := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == http.StatusUnprocessableEntity && strings.Contains(rec.Body.String(), "Strict budgets not released")
	}
	spec := strictFor("strict-release")
	if rec := e.Do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": "strict-unreleased"}); !unreleased(rec) {
		t.Fatalf("unreleased strict budget reached activation: %d %s", rec.Code, rec.Body)
	}
	monitored := financialDraftSpec()
	monitored.Enabled, monitored.Action, monitored.DrainSeconds = true, "notify", 0
	decodeFinancialBudget(t, e.Do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: monitored}, map[string]string{"Idempotency-Key": "monitored-unaffected"}), http.StatusCreated)

	nodes, err := store.ListComputeNodes(t.Context(), false)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("nodes: %v %v", nodes, err)
	}
	for _, node := range nodes {
		owner := runtimeadmission.Identity{NodeID: node.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
		if err := store.RegisterComputeNodeRuntimeIdentity(t.Context(), owner); err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterFinancialComputeOwner(t.Context(), owner.NodeID, owner.Incarnation); err != nil {
			t.Fatal(err)
		}
	}
	releaseStrictBudgets(e)
	p := decodeFinancialBudget(t, e.Do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": "strict-released"}), http.StatusCreated)

	(*e.S()).SeedPlatformFlag(platformflags.StrictBudgets, platformflags.Definition{})
	other := strictFor("strict-withdrawn")
	if rec := e.Do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: other}, map[string]string{"Idempotency-Key": "strict-withdrawn"}); !unreleased(rec) {
		t.Fatalf("withdrawn flag admitted a new strict budget: %d %s", rec.Code, rec.Body)
	}
	spec.LimitMillicents = 2000
	p = decodeFinancialBudget(t, e.Do(t, "PUT", "/v1/billing/budgets/"+p.ID, api.UpdateFinancialBudgetRequest{ExpectedRevision: p.Revision, Spec: spec}, map[string]string{"Idempotency-Key": "strict-raise-limit"}), http.StatusOK)
	spec.Enabled = false
	p = decodeFinancialBudget(t, e.Do(t, "PUT", "/v1/billing/budgets/"+p.ID, api.UpdateFinancialBudgetRequest{ExpectedRevision: p.Revision, Spec: spec}, map[string]string{"Idempotency-Key": "strict-disable"}), http.StatusOK)
	spec.Enabled = true
	if rec := e.Do(t, "PUT", "/v1/billing/budgets/"+p.ID, api.UpdateFinancialBudgetRequest{ExpectedRevision: p.Revision, Spec: spec}, map[string]string{"Idempotency-Key": "strict-reenable"}); !unreleased(rec) {
		t.Fatalf("withdrawn flag re-enabled a strict budget: %d %s", rec.Code, rec.Body)
	}
}
