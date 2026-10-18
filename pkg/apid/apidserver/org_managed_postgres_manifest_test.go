package apidserver_test

// adr: 202610171906

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgProjectManifestPostgresOwnership(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "manifest-db", 9001)
	ctx := t.Context()
	store := *env.Store()
	actor, err := store.AccountByID(ctx, *env.AcctID())
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "manifest-database-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, actor.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_manifest_db"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_manifest_db"); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: org.ID, OrgID: org.ID, Slug: "manifest-project"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, _, personalID := apidserver.ConfigureSourceRefManagedPostgres(t, env)
	service := *(*env.Srv()).ManagedPostgres()
	owned, err := service.DatabaseOwnership(ctx, personalID)
	if err != nil {
		t.Fatal(err)
	}
	owned.ID, owned.AccountID, owned.OrgID, owned.Name = uuid.NewString(), org.ID, org.ID, "company-db"
	owned.ProviderResourceID, owned.DataResourceID, owned.LeaseToken = "", "", ""
	owned.State, owned.DesiredGeneration, owned.ObservedGeneration = managedpostgres.StateProvisioning, 1, 0
	now := time.Now().UTC()
	owned.CreatedAt, owned.UpdatedAt, owned.RetryAt, owned.LeaseUntil = now, now, now, time.Time{}
	ownedCtx := managedpostgres.WithOwnership(ctx, org.ID, org.ID)
	if _, _, err := catalog.Reserve(ownedCtx, owned, 3); err != nil {
		t.Fatal(err)
	}
	claimed, err := catalog.Claim(ctx, org.ID, owned.ID, "ready", managedpostgres.StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordProviderResource(ctx, owned.ID, claimed.LeaseToken, "company-resource", now); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.FinishProvision(ctx, owned.ID, claimed.LeaseToken, now); err != nil {
		t.Fatal(err)
	}
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	limited, err := store.CreateOrgAPIKey(ctx, org.ID, actor.ID, hash, "deploy only", []string{api.ScopeDeployWrite}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, database string
		key            *state.APIKey
		want           int
	}{
		{"company database", "company-db", nil, 200},
		{"personal database hidden", "orders", nil, 404},
		{"deployment scope cannot attach", "company-db", &limited, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("databases:\n  - database: "+tc.database+"\n    app: worker\n    env: DATABASE_URL\n    access: read_write\n"), 0600); err != nil {
				t.Fatal(err)
			}
			next := func(w http.ResponseWriter, r *http.Request, owner state.Account) {
				resolved, problem := (*env.Srv()).LoadAndResolveProjectManifest(r.Context(), owner, dir, []string{"worker"}, "staging")
				if problem != nil {
					api.WriteProblem(w, problem)
					return
				}
				if len(resolved.PostgresBindings) != 1 {
					t.Errorf("resolved %d bindings", len(resolved.PostgresBindings))
				}
				w.WriteHeader(200)
			}
			wrapped := projectaccess.Wrap(store, nil, next)
			request := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+org.Slug+"/projects/"+project.Slug+"/deploy", nil)
			request.Pattern = "POST /v1/orgs/{organization}/projects/{slug}/deploy"
			request.SetPathValue("organization", org.Slug)
			request.SetPathValue("slug", project.Slug)
			request = request.WithContext(authmw.WithPrincipal(ctx, actor, tc.key, nil))
			response := httptest.NewRecorder()
			wrapped(response, request, actor)
			if response.Code != tc.want {
				t.Fatalf("manifest selection: %d %s; want %d", response.Code, response.Body, tc.want)
			}
		})
	}
}
