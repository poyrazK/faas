package migrationverify

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestConnectionTargetsRequireOwnedProjectAndExactLiveDeployment(t *testing.T) {
	for _, foreignProject := range []bool{false, true} {
		t.Run(map[bool]string{false: "same project", true: "another project"}[foreignProject], func(t *testing.T) {
			m := state.NewMemStore()
			ctx := t.Context()
			acct, err := m.CreateAccount(ctx, "connections@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := m.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
			if err != nil {
				t.Fatal(err)
			}
			targetProject := project.ID
			if foreignProject {
				p, e := m.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "other"})
				if e != nil {
					t.Fatal(e)
				}
				targetProject = p.ID
			}
			caller, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web", Type: state.AppTypeApp, Manifest: state.AppManifest{ServiceBindings: api.ServiceBindingsForTargets([]string{"api"})}})
			if err != nil {
				t.Fatal(err)
			}
			// A global slug fallback must not grant another project access.
			target, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: targetProject, Slug: "api", WorkloadName: "api", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			live := func(app string) string {
				t.Helper()
				d, e := m.CreateDeployment(ctx, state.Deployment{AppID: app, ImageDigest: "sha256:test"})
				if e != nil {
					t.Fatal(e)
				}
				if e = m.MarkDeploymentLive(ctx, d.ID); e != nil {
					t.Fatal(e)
				}
				id, e := uuid.Parse(d.ID)
				if e != nil {
					t.Fatal(e)
				}
				return id.String()
			}
			live(caller.ID)
			id := live(target.ID)
			connections := []api.MigrationServiceConnectionCheck{{EnvKey: "API_URL", Service: "api", DeploymentID: id}}
			err = ValidateConnectionTargets(ctx, m, acct.ID, caller.ID, connections)
			if foreignProject {
				if !errors.Is(err, state.ErrConflict) {
					t.Fatalf("foreign target accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			live(target.ID)
			if err := ValidateConnectionTargets(ctx, m, acct.ID, caller.ID, connections); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("replaced deployment accepted: %v", err)
			}
		})
	}
}
