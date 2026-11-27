// adr: 20261122220000 — verification contracts.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

const verificationContractJSON = `{"version":1,"checks":[{"name":"health","http":{"path":"/healthz"}},{"name":"orphans","sql":{"database":"main","query":"select count(*) from orders","expect":{"value":"0"}}}]}`

func TestVerificationContractRoutes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "shop"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	path := "/v1/apps/shop/verification-contract"
	put := api.PutVerificationContractRequest{Contract: json.RawMessage(verificationContractJSON)}
	// The route-level release is apid's capability gate (catalog route
	// /v1/apps/{}/verification-contract), covered by pkg/apid/capgate.
	(*e.S()).EnablePlatformFlagForAll(platformflags.CapabilityFlagKey("verification-contracts"))

	if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get before put: %d %s", rec.Code, rec.Body.String())
	}
	rec := e.Do(t, http.MethodPut, path, put, nil)
	var stored api.VerificationContract
	if err := json.Unmarshal(rec.Body.Bytes(), &stored); err != nil || rec.Code != http.StatusOK || stored.Revision != 1 || stored.CheckCount != 2 || len(stored.ContractSHA256) != 64 {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	stale := int64(0)
	if rec := e.Do(t, http.MethodPut, path, api.PutVerificationContractRequest{Contract: put.Contract, ExpectedRevision: &stale}, nil); rec.Code != http.StatusConflict {
		t.Fatalf("stale revision: %d %s", rec.Code, rec.Body.String())
	}
	invalid := api.PutVerificationContractRequest{Contract: json.RawMessage(`{"version":1,"checks":[{"name":"x","http":{"path":"/","headers":{"Host":"evil"}}}]}`)}
	if rec := e.Do(t, http.MethodPut, path, invalid, nil); rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("invalid contract: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodDelete, path+"?expected_revision=99", nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("stale delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodDelete, path+"?expected_revision=1", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d %s", rec.Code, rec.Body.String())
	}
}

type verifyQueryProvider struct {
	resizeAPIProvider
	value string
	sql   *[]string
}

func (verifyQueryProvider) IssueCredentials(_ context.Context, r managedpostgres.CredentialRequest) (managedpostgres.CredentialMaterial, error) {
	return managedpostgres.CredentialMaterial{ProviderIdentityID: "identity-" + r.IdentityKey, Username: "runtime", Password: "test-password", Database: "gregale", TLSMode: "require",
		Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointPooled, Host: "pool.example.test", Port: 5432}, {Role: managedpostgres.EndpointDirect, Host: "direct.example.test", Port: 5432}}}, nil
}

func (p verifyQueryProvider) QueryReadOnly(_ context.Context, req managedpostgres.ReadOnlyQueryRequest) (managedpostgres.ReadOnlyQueryResult, error) {
	*p.sql = append(*p.sql, req.SQL)
	if req.SQL == "broken" {
		return managedpostgres.ReadOnlyQueryResult{}, &managedpostgres.QueryError{SQLState: "42601", Message: "syntax error"}
	}
	v := p.value
	if req.SQL == "long" {
		// 1200 bytes of two-byte characters: a byte cut at 1024 would land
		// inside a character.
		v = strings.Repeat("é", 600)
	}
	return managedpostgres.ReadOnlyQueryResult{Columns: []managedpostgres.QueryColumn{{Name: "count", Type: "int8"}}, Rows: [][]*string{{&v}}, RowCount: 1}, nil
}

func TestVerificationQueryWorkerRunsReadOnlySQL(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	accountID := (*e.Acct()).ID
	rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "shop"}, nil)
	var app api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	var ran []string
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "private-backend"},
		Backends: []managedpostgres.BackendConfig{{ID: "private-backend", Driver: "fixture", Region: "us-east-1", Namespace: "private"}}},
		func(string) string { return "PRIVATE_SECRET" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return verifyQueryProvider{value: "0", sql: &ran}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	pgStore := managedpostgres.NewMemoryStore()
	service, err := managedpostgres.NewService(registry, pgStore, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := managedpostgres.NewBindingService(registry, pgStore, pgStore, customRolesAPISink{}, managedpostgres.BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) managedpostgres.Database {
		d, err := service.Create(t.Context(), managedpostgres.CreateRequest{AccountID: accountID, Name: name, Spec: managedpostgres.Spec{Region: "us-east-1", PostgresMajor: 17,
			Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	main := create("main")
	create("other")
	if _, err := bindings.Create(t.Context(), managedpostgres.CreateBindingRequest{AccountID: accountID, DatabaseID: main.ID, AppID: app.ID,
		Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite}); err != nil {
		t.Fatal(err)
	}
	*(*e.S()).ManagedPostgres() = service
	*(*e.S()).ManagedPostgresBindings() = bindings
	store := *e.Store()
	expires := time.Now().Add(time.Minute)
	queue := func(database, sql string) string {
		q, err := store.CreateVerificationQuery(t.Context(), state.VerificationQuery{DeploymentID: "dep", AppID: app.ID, AccountID: accountID, CheckName: "sql", Database: database, Query: sql, ExpiresAt: expires})
		if err != nil {
			t.Fatal(err)
		}
		return q.ID
	}
	ok, unbound, missing, broken := queue("main", "select count(*) from orders"), queue("other", "select 1"), queue("nope", "select 1"), queue("main", "broken")
	long := queue("main", "long")
	(*e.S()).RunVerificationQueriesOnce(t.Context())

	got, _ := store.VerificationQueryByID(t.Context(), ok)
	if got.Status != state.VerificationQueryDone || got.ErrorCode != "" || got.RowCount != 1 || !got.HasFirst || got.FirstValue == nil || *got.FirstValue != "0" {
		t.Fatalf("ok query = %+v", got)
	}
	for name, id := range map[string]string{"unbound": unbound, "missing": missing} {
		if got, _ := store.VerificationQueryByID(t.Context(), id); got.ErrorCode != verifycontract.CodeDatabaseNotFound {
			t.Fatalf("%s database = %+v", name, got)
		}
	}
	if got, _ := store.VerificationQueryByID(t.Context(), broken); got.ErrorCode != verifycontract.CodeQueryFailed {
		t.Fatalf("broken query = %+v", got)
	}
	if got, _ := store.VerificationQueryByID(t.Context(), long); got.FirstValue == nil || !utf8.ValidString(*got.FirstValue) || len(*got.FirstValue) > api.VerificationFirstValueMaxBytes {
		t.Fatalf("long value not cut on a character boundary: %+v", got)
	}
	if len(ran) != 3 {
		t.Fatalf("provider ran %q; only the bound database's three queries may run", ran)
	}
	for _, sql := range ran {
		if sql == "select 1" {
			t.Fatalf("an unbound or missing database was queried: %q", ran)
		}
	}
}
