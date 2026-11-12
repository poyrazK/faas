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

// Hunt 34 H34-15: activating an app's strict compute budget answered every
// failed condition with one paragraph. The store now names the condition:
// a node without a capable owner first, and activation succeeds once it holds.
func TestFinancialComputeActivationNamesTheFailedCondition(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store computeTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			ctx := t.Context()
			a := financialAccount(t, store)
			app, err := store.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "activation-reason", RAMMB: 256, MaxConcurrency: 2})
			if err != nil {
				t.Fatal(err)
			}
			spec := financialBudgetSpec()
			spec.Scope = financial.BudgetScope{Kind: "app", ID: app.ID}
			spec.Mode, spec.Action, spec.Basis, spec.ResumeRule = "strict", "suspend_workloads", "gross_usage", "manual"
			spec.NotifyMillicents = []int64{}
			spec.LimitMillicents = 1_000_000

			_, err = store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", spec)
			var unavailable *financial.ComputeUnavailableError
			if !errors.As(err, &unavailable) || unavailable.Reason != financial.ComputeUnavailableNodes || !errors.Is(err, financial.ErrComputeUnavailable) {
				t.Fatalf("activation without node owners = %v; want reason %q", err, financial.ComputeUnavailableNodes)
			}

			nodes, err := store.ListComputeNodes(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range nodes {
				i := runtimeadmission.Identity{NodeID: n.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
				if err := store.RegisterComputeNodeRuntimeIdentity(ctx, i); err != nil {
					t.Fatal(err)
				}
				if err := store.RegisterFinancialComputeOwner(ctx, i.NodeID, i.Incarnation); err != nil {
					t.Fatal(err)
				}
			}
			for at := app.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
				if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", spec); err != nil {
				t.Fatalf("activation with every condition met: %v", err)
			}
		})
	}
}
