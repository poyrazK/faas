// adr: 731 — authenticated durable PostgreSQL API restart and replay.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
	"github.com/onebox-faas/faas/pkg/state"
)

type durableAPIProvider struct {
	fault string
	spec  mp.Spec
}

func (p *durableAPIProvider) Capabilities() mp.Capabilities {
	return mp.Capabilities{PostgresMajors: []int{17}, ServiceClasses: []mp.ServiceClass{mp.ClassDevelopment}, Availability: []mp.Availability{mp.AvailabilitySingleZone}, CredentialAccess: []mp.CredentialAccess{mp.CredentialReadWrite, mp.CredentialMigration}, ScaleToZero: true, PooledConnections: true, PointInTimeRestore: true, MaxRestoreWindowSeconds: 7 * 24 * 60 * 60}
}

func (p *durableAPIProvider) Provision(ctx context.Context, r mp.ProvisionRequest) (mp.ObservedDatabase, error) {
	p.spec = r.Spec
	if p.fault == mp.QualificationProvisionAck {
		p.fault = ""
		return mp.ObservedDatabase{}, mp.ErrUnavailable
	}
	return mp.ObservedDatabase{ProviderResourceID: "provider-" + r.ResourceID, DataResourceID: "provider-" + r.ResourceID + "/branch", Status: mp.ProviderStatusReady, Spec: r.Spec}, nil
}

func (p *durableAPIProvider) IssueCredentials(ctx context.Context, r mp.CredentialRequest) (mp.CredentialMaterial, error) {
	hash := sha256.Sum256([]byte(r.IdentityKey))
	m := mp.CredentialMaterial{ProviderIdentityID: "identity-" + r.IdentityKey, Username: "runtime_" + hex.EncodeToString(hash[:8]), Password: "test-password", Database: "gregale", TLSMode: "require", Endpoints: []mp.Endpoint{{Role: mp.EndpointPooled, Host: "pool.example.test", Port: 5432}, {Role: mp.EndpointDirect, Host: "direct.example.test", Port: 5432}}}
	if p.fault == mp.QualificationCredentialAck {
		p.fault = ""
		return mp.CredentialMaterial{}, mp.ErrUnavailable
	}
	return m, nil
}

func (p *durableAPIProvider) Inspect(_ context.Context, id string) (mp.ObservedDatabase, error) {
	return mp.ObservedDatabase{ProviderResourceID: id, DataResourceID: id + "/branch", Status: mp.ProviderStatusReady, Spec: p.spec}, nil
}
func (*durableAPIProvider) Update(context.Context, mp.UpdateRequest) (mp.ObservedDatabase, error) {
	return mp.ObservedDatabase{}, mp.ErrUnsupported
}
func (*durableAPIProvider) Restore(context.Context, mp.RestoreRequest) (mp.ObservedDatabase, error) {
	return mp.ObservedDatabase{}, mp.ErrUnsupported
}
func (*durableAPIProvider) Delete(context.Context, mp.DeleteRequest) (mp.DeleteResult, error) {
	return mp.DeleteResult{Done: true}, nil
}
func (*durableAPIProvider) RevokeCredentials(context.Context, mp.CredentialRequest) error { return nil }
func (*durableAPIProvider) Usage(_ context.Context, _ string, w mp.UsageWindow) (mp.Usage, error) {
	return mp.Usage{Window: w}, nil
}

type durableAPISink struct {
	mp.CredentialSink
	fault bool
}

func (s *durableAPISink) Put(ctx context.Context, b mp.Binding, m mp.CredentialMaterial) (string, error) {
	ref, err := s.CredentialSink.Put(ctx, b, m)
	if err == nil && s.fault {
		s.fault = false
		return "", mp.ErrUnavailable
	}
	return ref, err
}

// This complements the real SQL workload tests: authenticated HTTP, persisted
// idempotency responses and production credential delivery survive rebuilding
// the server. Provider management is simulated; no guest deployment is claimed.
func TestManagedPostgresDurableAPIRestart(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@durable-api.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountEmailVerified(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, account.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "durable-api-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	hmac := []byte(strings.Repeat("h", 32))
	now := time.Now().UTC()
	var closeSession func()
	defer func() {
		if closeSession != nil {
			closeSession()
		}
	}()
	var handler http.Handler
	var catalog *mp.PostgresStore
	var observer credentialdelivery.Observer
	restart := func(fault string, enabled bool) {
		t.Helper()
		if closeSession != nil {
			closeSession()
		}
		now = now.Add(2 * time.Minute)
		at := now
		fresh, err := pgxpool.NewWithConfig(ctx, pool.Config().Copy())
		if err != nil {
			t.Fatal(err)
		}
		closeSession = fresh.Close
		secretStore := state.NewPgStore(fresh)
		catalog, err = mp.NewPostgresStore(fresh)
		if err != nil {
			t.Fatal(err)
		}
		provider := &durableAPIProvider{fault: fault}
		registry, err := mp.NewRegistry(mp.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, Backends: []mp.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "durable-api"}}}, func(string) string { return "" }, map[string]mp.Factory{"test": func(mp.BackendConfig, func(string) string) (mp.Provider, error) { return provider, nil }})
		if err != nil {
			t.Fatal(err)
		}
		service, err := mp.NewService(registry, catalog, mp.ServiceOptions{Now: func() time.Time { return at }, ProvisioningEnabled: func() bool { return enabled }})
		if err != nil {
			t.Fatal(err)
		}
		sink, err := newAppSecretCredentialSink(secretStore, func() *age.X25519Recipient { return identity.Recipient() }, func() []byte { return hmac })
		if err != nil {
			t.Fatal(err)
		}
		bindings, err := mp.NewBindingService(registry, catalog, catalog, &durableAPISink{CredentialSink: sink, fault: fault == mp.QualificationSecretAck}, mp.BindingServiceOptions{Now: func() time.Time { return at }, ProvisioningEnabled: func() bool { return enabled }})
		if err != nil {
			t.Fatal(err)
		}
		srv := newServerWithDeps(secretStore, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}, "", noopMailer{}, nil, nil, nil, 15*time.Minute, "")
		srv.managedPostgres = service
		srv.managedPostgresBindings = bindings
		handler = srv.handler()
		observer = credentialdelivery.Observer{Store: secretStore, Identity: identity, HMACKey: hmac}
	}
	request := func(method, path, idempotency string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		if idempotency != "" {
			req.Header.Set("Idempotency-Key", idempotency)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.Code, want, response.Body.String())
		}
		for _, secret := range []string{"test-password", "postgres://", "postgresql://", identity.String()} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("credential leaked over lifecycle API")
			}
		}
		return response
	}
	body := api.CreateManagedPostgresDatabaseRequest{Name: "durable-api", Region: "eu", PostgresMajor: 17, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 86400}
	restart(mp.QualificationProvisionAck, true)
	request(http.MethodPost, "/v1/postgres/databases", "create", body, http.StatusServiceUnavailable)
	rows, err := catalog.List(ctx, account.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("lost ack reservation: count=%d err=%v", len(rows), err)
	}
	databaseID := rows[0].ID
	restart("", true)
	created := request(http.MethodPost, "/v1/postgres/databases", "create", body, http.StatusCreated)
	var database api.ManagedPostgresDatabase
	if err := json.Unmarshal(created.Body.Bytes(), &database); err != nil {
		t.Fatal(err)
	}
	if database.ID != databaseID || database.State != "ready" {
		t.Fatal("HTTP retry replaced reserved database")
	}
	restart("", true)
	replayed := request(http.MethodPost, "/v1/postgres/databases", "create", body, http.StatusCreated)
	if replayed.Header().Get("Idempotent-Replayed") != "true" || replayed.Body.String() != created.Body.String() {
		t.Fatal("idempotency response did not survive restart")
	}
	bindingBody := api.CreateManagedPostgresBindingRequest{AppID: app.ID, Scope: "default", EnvironmentKey: "DATABASE_URL", Access: "read_write"}
	path := "/v1/postgres/databases/" + databaseID + "/bindings"
	restart(mp.QualificationCredentialAck, true)
	request(http.MethodPost, path, "bind", bindingBody, http.StatusServiceUnavailable)
	bindingRows, err := catalog.ListBindings(ctx, account.ID, databaseID)
	if err != nil || len(bindingRows) != 1 {
		t.Fatalf("lost credential ack: count=%d err=%v", len(bindingRows), err)
	}
	bindingID := bindingRows[0].ID
	restart(mp.QualificationSecretAck, true)
	request(http.MethodPost, path, "bind", bindingBody, http.StatusServiceUnavailable)
	restart("", true)
	bound := request(http.MethodPost, path, "bind", bindingBody, http.StatusCreated)
	var binding api.ManagedPostgresBinding
	if err := json.Unmarshal(bound.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	if binding.ID != bindingID || binding.State != "ready" {
		t.Fatal("HTTP retry replaced reserved binding")
	}
	current, err := catalog.GetBinding(ctx, account.ID, bindingID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := observer.URI(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	restart(mp.QualificationSecretAck, true)
	request(http.MethodPost, "/v1/postgres/bindings/"+bindingID+"/rotate", "rotate", nil, http.StatusServiceUnavailable)
	restart("", true)
	request(http.MethodPost, "/v1/postgres/bindings/"+bindingID+"/rotate", "rotate", nil, http.StatusOK)
	current, err = catalog.GetBinding(ctx, account.ID, bindingID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := observer.URI(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	if current.CredentialGeneration != 2 || after == before {
		t.Fatal("HTTP rotation lost generation or did not publish replacement")
	}
	restart("", false)
	request(http.MethodDelete, "/v1/postgres/bindings/"+bindingID, "delete-binding", nil, http.StatusOK)
	request(http.MethodDelete, "/v1/postgres/databases/"+databaseID, "delete-database", nil, http.StatusOK)
	restart("", false)
	if err := observer.Deleted(ctx, current); err != nil {
		t.Fatal(err)
	}
	deleted, err := catalog.Get(ctx, account.ID, databaseID)
	if err != nil || deleted.State != mp.StateDeleted {
		t.Fatalf("durable cleanup: state=%s err=%v", deleted.State, err)
	}
	if _, err := state.NewPgStore(pool).GetAppSecretInScope(ctx, account.ID, app.ID, "default", "DATABASE_URL"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cleanup left managed credential")
	}
}
