package state_test

// adr: 20261102205122

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

type reaperBulkReadsTestStore interface {
	state.DeploymentsByIDsReader
	state.FinancialBudgetAccountsReader
	state.FinancialBudgetStore
}

func TestMemReaperBulkReads(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	seed := func(email string) (string, string) {
		t.Helper()
		acct, err := m.CreateAccount(ctx, email, api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "bulk-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:bulk", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		return acct.ID, dep.ID
	}
	plainAccount, plainDeployment := seed("bulk-plain@example.com")
	budgetAccount, budgetDeployment := seed("bulk-budget@example.com")
	testReaperBulkReads(t, m, plainAccount, budgetAccount, []string{plainDeployment, budgetDeployment}, uuid.NewString())
}

// The bulk reads answer what the per-row reads answer: every existing
// deployment's row, and exactly the accounts with an undeleted budget policy.
func testReaperBulkReads(t *testing.T, s reaperBulkReadsTestStore, plainAccount, budgetAccount string, deployments []string, missing string) {
	t.Helper()
	ctx := context.Background()
	got, err := s.DeploymentsByIDs(ctx, append(append([]string{}, deployments...), missing))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(deployments) {
		t.Fatalf("DeploymentsByIDs = %d rows, want %d", len(got), len(deployments))
	}
	for _, id := range deployments {
		if got[id].ID != id {
			t.Fatalf("DeploymentsByIDs missing %s: %+v", id, got)
		}
	}

	accounts := []string{plainAccount, budgetAccount}
	if with, err := s.AccountsWithFinancialBudgets(ctx, accounts); err != nil || len(with) != 0 {
		t.Fatalf("before any budget: %v, %v", with, err)
	}
	spec := financial.BudgetSpec{Name: "Compute", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"},
		Basis: "gross_usage", LimitMillicents: 100, Mode: "monitored", Action: "stop_previews", ResumeRule: "manual", Enabled: true}
	if _, err := s.CreateFinancialBudget(ctx, budgetAccount, uuid.NewString(), "editor", spec); err != nil {
		t.Fatal(err)
	}
	with, err := s.AccountsWithFinancialBudgets(ctx, accounts)
	if err != nil || len(with) != 1 || !with[budgetAccount] {
		t.Fatalf("AccountsWithFinancialBudgets = %v, %v; want only %s", with, err, budgetAccount)
	}
}
