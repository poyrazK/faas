// adr: 202610171906

package postgresaccess_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/postgresaccess"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// These tests use only catalogue metadata. The wrapper must not invoke any
// provider lifecycle method while resolving ownership or denying access.
type metadataProvider struct{ managedpostgres.Provider }

func (metadataProvider) Capabilities() managedpostgres.Capabilities {
	return managedpostgres.Capabilities{PostgresMajors: []int{17}, ServiceClasses: []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment}, Availability: []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone}, ScaleToZero: true, PooledConnections: true, CredentialAccess: []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite}, UsageMeters: []managedpostgres.Meter{managedpostgres.MeterComputeUnitSeconds}, MaxStorageBytes: 1 << 30}
}

func TestOffboardingFencesIndependentMemoryCatalogue(t *testing.T) {
	ctx := t.Context()
	identities := state.NewMemStore()
	owner, err := identities.CreateAccount(ctx, "owner@postgres-access.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identities.CreateAccount(ctx, "actor@postgres-access.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := identities.CreateOrg(ctx, state.Org{Slug: "postgres-access", Name: "Company", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		id   string
		role state.OrgRole
	}{{owner.ID, state.OrgRoleOwner}, {actor.ID, state.OrgRoleAdmin}} {
		if err := identities.AddOrgMember(ctx, org.ID, m.id, m.role, nil); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "metadata"}, Backends: []managedpostgres.BackendConfig{{ID: "metadata", Driver: "metadata", Region: "us-east-1", Namespace: "test"}}}, func(string) string { return "" }, map[string]managedpostgres.Factory{"metadata": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
		return metadataProvider{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	catalog := managedpostgres.NewMemoryStore()
	service, err := managedpostgres.NewService(registry, catalog, managedpostgres.ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	database := managedpostgres.Database{ID: uuid.NewString(), AccountID: org.ID, OrgID: org.ID, Name: "revoked", BackendID: "metadata", BackendFingerprint: strings.Repeat("a", 64), State: managedpostgres.StateProvisioning, CreatedAt: time.Now().UTC()}
	wrapped := postgresaccess.Wrap(identities, nil, func() *managedpostgres.Service { return service }, func(w http.ResponseWriter, r *http.Request, resource state.Account) {
		if resource.ID != org.ID {
			t.Error("handler did not receive the resource namespace")
		}
		principal, _, ok := authmw.AccountFromContext(r)
		if !ok || principal.ID != actor.ID {
			t.Error("resource owner replaced the human principal")
		}
		close(entered)
		<-release
		if _, _, err := catalog.Reserve(r.Context(), database, 2); err != nil {
			api.WriteProblem(w, api.ErrCapacity("reservation failed"))
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+org.Slug+"/postgres/databases", nil)
	request.Pattern = "POST /v1/orgs/{organization}/postgres/databases"
	request.SetPathValue("organization", org.Slug)
	request = request.WithContext(authmw.WithPrincipal(ctx, actor, nil, nil))
	response := httptest.NewRecorder()
	go func() { defer close(done); wrapped(response, request, actor) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not reach catalogue barrier")
	}
	if err := identities.RemoveOrgMember(ctx, org.ID, actor.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("revoked request did not finish")
	}
	if response.Code != http.StatusForbidden {
		t.Fatalf("revoked catalogue write: %d %s", response.Code, response.Body)
	}
	if _, err := catalog.DatabaseOwnership(ctx, database.ID); err == nil {
		t.Fatal("removed member committed independent catalogue intent")
	}
}

func TestUnreviewedPostgresActionsStayClosed(t *testing.T) {
	for _, pattern := range []string{"POST /v1/postgres/cutovers", "GET /v1/orgs/{organization}/postgres/cutovers/{id}", "POST /v1/postgres/databases/{id}/new-operation"} {
		if _, ok := postgresaccess.Action(pattern); ok {
			t.Fatalf("unreviewed action authorized: %s", pattern)
		}
	}
}

func TestManifestBindingsRequireDatabaseScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		role   state.OrgRole
		scopes []string
		want   int
	}{
		{"deployment key", state.OrgRoleDeveloper, []string{api.ScopeDeployWrite}, 403},
		{"database management key", state.OrgRoleDeveloper, []string{api.ScopeManagedPostgresManage}, 0},
		{"viewer management key", state.OrgRoleViewer, []string{api.ScopeManagedPostgresManage}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor := state.Account{ID: "human"}
			member := state.OrgMembership{OrgID: "company", AccountID: actor.ID, Role: tc.role}
			ctx := authmw.WithPrincipal(t.Context(), actor, &state.APIKey{OrgID: member.OrgID, AccountID: actor.ID, Scopes: tc.scopes}, &member)
			request := httptest.NewRequest(http.MethodPost, "/v1/orgs/company/projects/project/deploy", nil).WithContext(ctx)
			ctx = authz.WithRequestOnContext(ctx, request)
			problem := postgresaccess.AuthorizeManifestBindings(ctx, nil)
			if tc.want == 0 {
				if problem != nil {
					t.Fatal(problem)
				}
				return
			}
			if problem == nil || problem.Status != tc.want {
				t.Fatalf("manifest scope/role: %+v", problem)
			}
		})
	}
}
