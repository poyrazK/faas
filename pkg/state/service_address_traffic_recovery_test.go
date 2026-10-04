// adr: 570 (ADR-576 private service addresses)
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Address allocation is part of committing app intent. An abandoned traffic
// analysis must leave both the app topology and the allocation cursor intact.
func TestMemServiceAddressAllocationAfterTrafficValidation(t *testing.T) {
	for _, mode := range []string{"create", "quota", "project-plan", "project-reconcile", "preview-batch"} {
		t.Run(mode, func(t *testing.T) {
			m := state.NewMemStore()
			ctx := t.Context()
			account, err := m.CreateAccount(ctx, "address-traffic@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			base, err := m.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "base", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			if index, err := m.AppServiceAddressIndex(ctx, base.ID); err != nil || index != 1 {
				t.Fatalf("initial index=(%d,%v), want 1", index, err)
			}
			limits, ok := api.LimitsFor(api.PlanPro)
			if !ok {
				t.Fatal("missing Pro limits")
			}
			app := state.App{AccountID: account.ID, Slug: "candidate", WorkloadName: "candidate", Status: state.AppActive}
			apply := func(c context.Context) ([]state.App, error) { a, e := m.CreateApp(c, app); return []state.App{a}, e }
			switch mode {
			case "quota":
				apply = func(c context.Context) ([]state.App, error) {
					a, e := m.CreateAppIfUnderQuota(c, app, limits)
					return []state.App{a}, e
				}
			case "project-plan":
				project := state.Project{AccountID: account.ID, Slug: "candidate-project"}
				apply = func(c context.Context) ([]state.App, error) {
					_, a, _, e := m.ApplyProjectPlan(c, project, []state.App{app}, nil, limits)
					return a, e
				}
			case "project-reconcile":
				project, e := m.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "candidate-project"})
				if e != nil {
					t.Fatal(e)
				}
				apply = func(c context.Context) ([]state.App, error) {
					r, e := m.ApplyProjectReconcile(c, project, []state.ProjectReconcileMutation{{Op: "create", App: app}}, nil, "", limits)
					return r.Added, e
				}
			case "preview-batch":
				app.PreviewOfSlug, app.PreviewPrNumber = "base", 7
				apply = func(c context.Context) ([]state.App, error) {
					return m.CreatePRPreviewAppsIfUnderQuota(c, []state.App{app}, limits)
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := apply(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled mutation error=%v", err)
			}
			if _, err := m.AppBySlug(ctx, app.Slug); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("refused app became visible: %v", err)
			}
			if index, err := m.AppServiceAddressIndex(ctx, base.ID); err != nil || index != 1 {
				t.Fatalf("refusal changed existing index=(%d,%v)", index, err)
			}
			accepted, err := apply(ctx)
			if err != nil || len(accepted) != 1 {
				t.Fatalf("recovery apps=%v err=%v", accepted, err)
			}
			index, err := m.AppServiceAddressIndex(ctx, accepted[0].ID)
			if err != nil || index != 2 {
				t.Fatalf("recovery index=(%d,%v), want 2 without an allocation for refused intent", index, err)
			}
			resolved, err := m.AppByServiceAddressIndex(ctx, account.ID, index)
			if err != nil || resolved.ID != accepted[0].ID {
				t.Fatalf("committed address resolved=%v err=%v", resolved, err)
			}
		})
	}
}
