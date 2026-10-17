//go:build !no_pg

package apidserver_test

// adr: 202610171906

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgManagedPostgresPostgresHTTP(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "")
	t.Setenv("FAAS_APPLICATION_AUTH_ACCOUNTS", "")
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store, creator, ctx := *e.Store(), *e.Acct(), t.Context()
	if err := store.MarkAccountEmailVerified(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	fixture := postgresfixture.New(t, *e.Pool(), identity, []byte(strings.Repeat("h", 32)), "", 0)
	(*e.S()).WithManagedPostgres(fixture.Service, nil, fixture.Bindings, nil, nil, nil)
	org, err := store.CreateOrg(ctx, state.Org{Slug: "postgres-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_postgres_company"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_postgres_company"); err != nil {
		t.Fatal(err)
	}
	admin, adminHeaders := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	developer, developerHeaders := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewerHeaders := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	_, billingHeaders := sharedAppMember(t, store, org, state.OrgRoleBilling, api.ScopesAdminOnly)
	readKey, readHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOrgAPIKey(ctx, org.ID, admin.ID, readHash, "read only", []string{api.ScopeManagedPostgresRead}, nil); err != nil {
		t.Fatal(err)
	}
	readHeaders := map[string]string{"Authorization": "Bearer " + readKey}
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "postgres-company-app"})
	if err != nil {
		t.Fatal(err)
	}
	personalApp, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, Slug: "postgres-personal-app"})
	if err != nil {
		t.Fatal(err)
	}
	spec := mp.Spec{Region: "us-east-1", PostgresMajor: fixture.Major, Class: mp.ClassDevelopment, Availability: mp.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30}
	// Each SQL qualification fixture models one provider database.
	personalFixture := postgresfixture.New(t, *e.Pool(), identity, []byte(strings.Repeat("p", 32)), "", 0)
	personal, err := personalFixture.Service.Create(ctx, mp.CreateRequest{AccountID: creator.ID, Name: "personal-db", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	companyCtx := mp.WithOwnership(ctx, org.ID, org.ID)
	database, err := fixture.Service.Create(companyCtx, mp.CreateRequest{AccountID: creator.ID, Name: "orders", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := fixture.Bindings.Create(companyCtx, mp.CreateBindingRequest{AccountID: creator.ID, DatabaseAccountID: database.AccountID, DatabaseID: database.ID, AppID: app.ID, Scope: "default", EnvironmentKey: "DATABASE_URL", Access: mp.CredentialReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	beforeURI, err := fixture.Observer.URI(ctx, binding)
	if err != nil {
		t.Fatal("read sealed company binding:", err)
	}
	beforeSecret, err := store.GetAppSecretInScope(ctx, creator.ID, app.ID, "default", "DATABASE_URL")
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/orgs/" + org.Slug + "/postgres"
	request := api.CreateManagedPostgresBindingRequest{AppID: app.ID, Scope: "staging", EnvironmentKey: "DATABASE_URL", Access: "read_write"}
	authRequest := request
	authRequest.Access = "auth"
	for _, tc := range []struct {
		method, path string
		body         any
		headers      map[string]string
		want         int
	}{
		{"GET", base + "/databases", nil, viewerHeaders, 200},
		{"GET", base + "/databases/" + database.ID, nil, developerHeaders, 200},
		{"GET", base + "/databases/" + database.ID, nil, billingHeaders, 403},
		{"GET", base + "/usage", nil, billingHeaders, 200},
		{"POST", base + "/databases", nil, developerHeaders, 403},
		{"POST", base + "/databases/" + database.ID + "/restore", nil, developerHeaders, 403},
		{"POST", base + "/databases/" + database.ID + "/resize", nil, developerHeaders, 403},
		{"DELETE", base + "/databases/" + database.ID, nil, developerHeaders, 403},
		{"POST", base + "/bindings/" + binding.ID + "/rotate", nil, developerHeaders, 403},
		{"POST", base + "/databases/" + database.ID + "/bindings", request, viewerHeaders, 403},
		{"POST", base + "/databases/" + database.ID + "/bindings", request, readHeaders, 403},
		{"POST", base + "/databases/" + database.ID + "/bindings", authRequest, developerHeaders, 400},
		{"GET", base + "/databases/" + personal.ID, nil, adminHeaders, 404},
		{"GET", "/v1/postgres/databases/" + personal.ID, nil, developerHeaders, 404},
	} {
		rec := e.Do(t, tc.method, tc.path, tc.body, tc.headers)
		if rec.Code != tc.want {
			t.Fatalf("%s %s: %d %s; want %d", tc.method, tc.path, rec.Code, rec.Body, tc.want)
		}
	}
	inventory := e.Do(t, "GET", base+"/databases", nil, viewerHeaders)
	if strings.Contains(inventory.Body.String(), personal.ID) || !strings.Contains(inventory.Body.String(), database.ID) {
		t.Fatal("company inventory mixed ownership")
	}
	personalInventory := e.Do(t, "GET", "/v1/postgres/databases", nil, nil)
	if personalInventory.Code != 200 || strings.Contains(personalInventory.Body.String(), database.ID) {
		t.Fatal("personal inventory exposed company database")
	}
	request.AppID = personalApp.ID
	if rec := e.Do(t, "POST", base+"/databases/"+database.ID+"/bindings", request, developerHeaders); rec.Code != 404 {
		t.Fatalf("cross-org app accepted: %d %s", rec.Code, rec.Body)
	}
	request.AppID = app.ID
	// A new app uses the company namespace while its database retains the
	// creator's original storage namespace.
	newApp, err := store.CreateApp(ctx, state.App{AccountID: org.ID, OrgID: org.ID, Slug: "new-postgres-company-app"})
	if err != nil {
		t.Fatal(err)
	}
	request.AppID = newApp.ID
	created := api.ManagedPostgresDatabase{ID: database.ID}
	rec := e.Do(t, "POST", base+"/databases/"+created.ID+"/bindings", request, developerHeaders)
	if rec.Code != 201 {
		t.Fatalf("cross-namespace company binding: %d %s", rec.Code, rec.Body)
	}
	var bound api.ManagedPostgresBinding
	if err := json.Unmarshal(rec.Body.Bytes(), &bound); err != nil {
		t.Fatal(err)
	}
	crossBinding, err := fixture.Catalog.BindingOwnership(ctx, bound.ID)
	if err != nil || crossBinding.AccountID != org.ID || crossBinding.DatabaseAccountID != creator.ID {
		t.Fatalf("binding namespaces: %+v %v", crossBinding, err)
	}
	if err := fixture.Observer.Credential(ctx, crossBinding); err != nil {
		t.Fatal("cross-namespace credential unavailable:", err)
	}
	// Legacy audit rows must not enter the creator's personal export.
	for _, data := range []map[string]any{{"database_id": database.ID, "marker": "company-database-audit"}, {"binding_id": binding.ID, "marker": "company-binding-audit"}} {
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "managed_postgres.legacy", &creator.ID, encoded); err != nil {
			t.Fatal(err)
		}
	}
	exported := e.Do(t, "GET", "/v1/account/export", nil, nil)
	if exported.Code != 200 || strings.Contains(exported.Body.String(), "company-database-audit") || strings.Contains(exported.Body.String(), "company-binding-audit") {
		t.Fatalf("personal export leaked database audit: status %d", exported.Code)
	}
	if err := store.TransferOrgOwnership(ctx, org.ID, creator.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, "GET", "/v1/postgres/databases/"+database.ID, nil, nil); rec.Code != 404 {
		t.Fatalf("departed creator accessed database: %d %s", rec.Code, rec.Body)
	}
	if _, err := personalFixture.Service.Delete(ctx, creator.ID, personal.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, "GET", base+"/databases/"+database.ID, nil, developerHeaders); rec.Code != 200 {
		t.Fatalf("creator erasure broke database: %d %s", rec.Code, rec.Body)
	}
	afterSecret, err := store.GetAppSecretInScope(ctx, creator.ID, app.ID, "default", "DATABASE_URL")
	if err != nil {
		t.Fatal(err)
	}
	if beforeSecret.ManagedCredentialRef != afterSecret.ManagedCredentialRef || !bytes.Equal(beforeSecret.Ciphertext, afterSecret.Ciphertext) {
		t.Fatal("creator erasure changed sealed credential")
	}
	afterURI, err := fixture.Observer.URI(ctx, binding)
	if err != nil || afterURI != beforeURI {
		t.Fatal("creator erasure changed database access")
	}
	if err := fixture.Observer.Credential(ctx, binding); err != nil {
		t.Fatal("retained SQL credential failed:", err)
	}
	image := e.Do(t, "POST", "/v1/apps/"+app.Slug+"/deployments", api.CreateDeploymentRequest{Image: "r/x@sha256:" + strings.Repeat("c", 64)}, developerHeaders)
	if image.Code != 202 {
		t.Fatalf("teammate deploy with retained binding: %d %s", image.Code, image.Body)
	}
	if rec := e.Do(t, "DELETE", "/v1/orgs/"+org.Slug, nil, adminHeaders); rec.Code != 409 {
		t.Fatalf("organization deletion orphaned a live database: %d %s", rec.Code, rec.Body)
	}
	rotation := e.Do(t, "POST", base+"/bindings/"+crossBinding.ID+"/rotate", nil, adminHeaders)
	if rotation.Code != 200 {
		t.Fatalf("remaining admin could not rotate credentials: %d %s", rotation.Code, rotation.Body)
	}
	rotated, err := fixture.Catalog.BindingOwnership(ctx, crossBinding.ID)
	if err != nil || rotated.CredentialGeneration != crossBinding.CredentialGeneration+1 || rotated.DatabaseAccountID != creator.ID {
		t.Fatalf("rotation changed binding identity: %+v %v", rotated, err)
	}
	if err := fixture.Observer.Credential(ctx, rotated); err != nil {
		t.Fatal("rotated company SQL credential failed:", err)
	}
	// A demotion applies before a cached successful binding request is replayed.
	developerHeaders["Idempotency-Key"] = "postgres-binding-replay"
	request.Scope = "preview"
	rec = e.Do(t, "POST", base+"/databases/"+created.ID+"/bindings", request, developerHeaders)
	if rec.Code != 201 {
		t.Fatalf("binding receipt: %d %s", rec.Code, rec.Body)
	}
	if err := store.UpdateOrgMemberRole(ctx, org.ID, developer.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	rec = e.Do(t, "POST", base+"/databases/"+created.ID+"/bindings", request, developerHeaders)
	if rec.Code != 403 {
		t.Fatalf("cached binding bypassed demotion: %d %s", rec.Code, rec.Body)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, developer.ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, "GET", base+"/databases/"+created.ID, nil, developerHeaders); rec.Code < 400 {
		t.Fatal("removed member retained access")
	}
}
