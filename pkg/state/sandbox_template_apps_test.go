//go:build !no_pg

// adr: 20261027090013 — sandbox template build apps count in their own pool
// (SandboxTemplatesMax), not DeployedApps, and stay out of ListApps and
// ListAppsByOrg, on both stores.
package state

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSandboxTemplateAppsHaveTheirOwnPool(t *testing.T) {
	stores := map[string]func(t *testing.T) Store{
		"mem": func(*testing.T) Store { return NewMemStore() },
		"pg":  func(t *testing.T) Store { return newPgStore(t) },
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := context.Background()
			acct, err := s.CreateAccount(ctx, "sbt-"+uuid.NewString()[:8]+"@example.com", api.PlanHobby)
			if err != nil {
				t.Fatal(err)
			}
			limits := api.MustLimitsFor(api.PlanHobby)
			max := api.SandboxTemplatesMax[api.PlanHobby.PlanIndex()]
			template := func(i int) App {
				return App{AccountID: acct.ID, Slug: "sbt-" + uuid.NewString()[:12], Status: AppActive, SandboxTemplate: true,
					WorkloadClass: WorkloadClassJob}
			}
			for i := 0; i < max; i++ {
				created, err := s.CreateAppIfUnderQuota(ctx, template(i), limits)
				if err != nil || !created.SandboxTemplate {
					t.Fatalf("template app %d = %+v, %v", i, created, err)
				}
			}
			var qe *QuotaError
			if _, err := s.CreateAppIfUnderQuota(ctx, template(max), limits); !errors.As(err, &qe) ||
				qe.Kind != QuotaErrorKindSandboxTemplates || qe.Limit != max || qe.Observed != max {
				t.Fatalf("template app past the pool = %v", err)
			}
			for i := 0; i < limits.DeployedApps; i++ {
				if _, err := s.CreateAppIfUnderQuota(ctx, App{AccountID: acct.ID, Slug: "app-" + uuid.NewString()[:12], Status: AppActive}, limits); err != nil {
					t.Fatalf("app %d after the template pool filled: %v", i, err)
				}
			}
			if n, _ := s.CountDeployedApps(ctx, acct.ID); n != limits.DeployedApps {
				t.Fatalf("CountDeployedApps = %d, want %d", n, limits.DeployedApps)
			}
			listed, err := s.ListApps(ctx, acct.ID)
			if err != nil || len(listed) != limits.DeployedApps {
				t.Fatalf("ListApps = %d apps, %v", len(listed), err)
			}
			if byOrgStore, ok := s.(interface {
				ListAppsByOrg(context.Context, string) ([]App, error)
			}); ok && listed[0].OrgID != "" {
				byOrg, err := byOrgStore.ListAppsByOrg(ctx, listed[0].OrgID)
				if err != nil || len(byOrg) != limits.DeployedApps {
					t.Fatalf("ListAppsByOrg = %d apps, %v", len(byOrg), err)
				}
			}
			for _, a := range listed {
				if a.SandboxTemplate {
					t.Fatalf("ListApps returned template app %s", a.Slug)
				}
			}
		})
	}
}
