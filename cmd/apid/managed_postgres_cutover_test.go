// adr: 391 — authenticated cutover transport preserves staging and tenant boundaries.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func cutoverAPIFixture(t *testing.T) (sourceRefTestEnv, *managedpostgres.CutoverService, api.PrepareManagedPostgresCutoverRequest) {
	t.Helper()
	ctx := context.Background()
	env := newSourceRefTestServer(t, api.PlanPro, "cutover-api", 7777)
	store, _, sourceID := configureSourceRefManagedPostgres(t, env)
	source, err := store.Get(ctx, env.acctID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := source
	target.ID = uuid.NewString()
	target.Name = "restore-target"
	target.State = managedpostgres.StateProvisioning
	target.ProviderResourceID = ""
	target.RestoreSourceDatabaseID = source.ID
	target.RestoreSourceResourceID = source.ProviderResourceID
	target.RestorePointInTime = now
	target, _, err = store.Reserve(ctx, target, 10)
	if err != nil {
		t.Fatal(err)
	}
	target, err = store.Claim(ctx, env.acctID, target.ID, "restore", managedpostgres.StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordProviderResource(ctx, target.ID, "restore", "private-target", now); err != nil {
		t.Fatal(err)
	}
	target, err = store.FinishProvision(ctx, target.ID, "restore", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialMigration} {
		key := "DATABASE_URL"
		if access == managedpostgres.CredentialMigration {
			key = "MIGRATION_DATABASE_URL"
		}
		_, err = env.srv.managedPostgresBindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: env.acctID, AppID: env.appID, DatabaseID: source.ID, Scope: "default", EnvironmentKey: key, Access: access})
		if err != nil {
			t.Fatal(err)
		}
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sink, err := newAppSecretCredentialSink(env.store, func() *age.X25519Recipient { return identity.Recipient() }, func() []byte { return []byte("test-key") })
	if err != nil {
		t.Fatal(err)
	}
	sink.identities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	sink.probe = func(_ context.Context, _ string, _ managedpostgres.CredentialAccess, major int) error {
		if major != 17 {
			t.Fatal(major)
		}
		return nil
	}
	service, err := managedpostgres.NewCutoverService(env.srv.managedPostgresBindings, store, sink)
	if err != nil {
		t.Fatal(err)
	}
	env.srv.managedPostgresCutovers = service
	return env, service, api.PrepareManagedPostgresCutoverRequest{SourceDatabaseID: source.ID, TargetDatabaseID: target.ID, AppID: env.appID, Scope: "default"}
}
func cutoverAPIGet(env sourceRefTestEnv, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/postgres/cutovers/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+env.key)
	w := httptest.NewRecorder()
	env.h.ServeHTTP(w, req)
	return w
}
func decodeCutoverAPI(t *testing.T, w *httptest.ResponseRecorder, status int) api.ManagedPostgresCutover {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cached cutover status")
	}
	for _, private := range []string{"test-password", "ciphertext", "private-target", "provider_identity", "lease_token", "backend_fingerprint"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private material exposed")
		}
	}
	var out api.ManagedPostgresCutover
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestManagedPostgresCutoverAPIStagesVerifiesAndCancels(t *testing.T) {
	env, service, req := cutoverAPIFixture(t)
	ctx := context.Background()
	c := decodeCutoverAPI(t, env.postWithIdempotency(t, "/v1/postgres/cutovers", req, "cutover-preparation"), http.StatusAccepted)
	repeat := decodeCutoverAPI(t, env.postWithIdempotency(t, "/v1/postgres/cutovers", req, "cutover-preparation"), http.StatusAccepted)
	if c.ID != repeat.ID || c.State != "preparing" || len(c.Members) != 2 || c.VerificationFresh {
		t.Fatal("bad preparation")
	}
	if w := env.post(t, "/v1/postgres/cutovers/"+c.ID+"/verify", nil); w.Code != http.StatusConflict {
		t.Fatal("unprepared target verified", w.Code)
	}
	invalid := map[string]any{"source_database_id": req.SourceDatabaseID, "target_database_id": req.TargetDatabaseID, "app_id": req.AppID, "scope": req.Scope, "unknown": true}
	if w := env.post(t, "/v1/postgres/cutovers", invalid); w.Code != http.StatusBadRequest {
		t.Fatal("unknown field accepted", w.Code)
	}
	for range 2 {
		if _, err := service.Reconcile(ctx, env.acctID, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	c = decodeCutoverAPI(t, cutoverAPIGet(env, c.ID), http.StatusOK)
	if c.State != "prepared" {
		t.Fatal(c.State)
	}
	c = decodeCutoverAPI(t, env.post(t, "/v1/postgres/cutovers/"+c.ID+"/verify", nil), http.StatusAccepted)
	if c.State != "verifying" {
		t.Fatal(c.State)
	}
	if _, err := service.Reconcile(ctx, env.acctID, c.ID); err != nil {
		t.Fatal(err)
	}
	c = decodeCutoverAPI(t, cutoverAPIGet(env, c.ID), http.StatusOK)
	if c.State != "verified" || !c.VerificationFresh || c.VerifiedAt == "" || c.VerificationMaxAgeSeconds != 300 {
		t.Fatal("verification evidence absent")
	}
	for _, m := range c.Members {
		if m.VerifiedAt == "" {
			t.Fatal("member timestamp absent")
		}
		if _, err := env.store.GetAppSecretInScope(ctx, env.acctID, env.appID, "default", m.EnvironmentKey); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("target secret was published", err)
		}
	}
	c = decodeCutoverAPI(t, env.post(t, "/v1/postgres/cutovers/"+c.ID+"/cancel", nil), http.StatusAccepted)
	if c.VerificationFresh || c.VerifiedAt != "" || c.State != "cancelling" {
		t.Fatal("cancellation kept evidence")
	}
	for range 2 {
		if _, err := service.Reconcile(ctx, env.acctID, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	if out := decodeCutoverAPI(t, cutoverAPIGet(env, c.ID), http.StatusOK); out.State != "cancelled" {
		t.Fatal(out.State)
	}
}
func TestManagedPostgresCutoverAPIScopeAndTenant(t *testing.T) {
	env, _, req := cutoverAPIFixture(t)
	c := decodeCutoverAPI(t, env.post(t, "/v1/postgres/cutovers", req), http.StatusAccepted)
	ctx := context.Background()
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = env.store.CreateAPIKey(ctx, env.acctID, hash, "read-cutover", []string{api.ScopeManagedPostgresRead}); err != nil {
		t.Fatal(err)
	}
	env.key = key
	if w := env.post(t, "/v1/postgres/cutovers/"+c.ID+"/verify", nil); w.Code != http.StatusForbidden {
		t.Fatal("read key mutated cutover", w.Code)
	}
	decodeCutoverAPI(t, cutoverAPIGet(env, c.ID), http.StatusOK)
	other, err := env.store.CreateAccount(ctx, "other-cutover@test.invalid", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err = api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = env.store.CreateAPIKey(ctx, other.ID, hash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	env.key = key
	if w := cutoverAPIGet(env, c.ID); w.Code != http.StatusNotFound {
		t.Fatal("cross-tenant read", w.Code)
	}
	if w := env.post(t, "/v1/postgres/cutovers/"+c.ID+"/cancel", nil); w.Code != http.StatusNotFound {
		t.Fatal("cross-tenant cancel", w.Code)
	}
	if w := env.post(t, "/v1/postgres/cutovers", req); w.Code != http.StatusNotFound {
		t.Fatal("cross-tenant app", w.Code)
	}
	if w := cutoverAPIGet(env, "invalid-uuid"); w.Code != http.StatusBadRequest {
		t.Fatal("invalid UUID", w.Code)
	}
}
func TestManagedPostgresCredentialVerifierRejectsEnvelopeDrift(t *testing.T) {
	env, _, _ := cutoverAPIFixture(t)
	identity, _ := age.GenerateX25519Identity()
	sink, _ := newAppSecretCredentialSink(env.store, func() *age.X25519Recipient { return identity.Recipient() }, func() []byte { return []byte("test-key") })
	sink.identities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	calls := 0
	sink.probe = func(_ context.Context, _ string, _ managedpostgres.CredentialAccess, _ int) error {
		calls++
		return nil
	}
	binding := managedpostgres.Binding{ID: uuid.NewString(), DatabaseID: "target", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite, CredentialGeneration: 1}
	target := managedpostgres.Database{ID: "target", Spec: managedpostgres.Spec{PostgresMajor: 17}}
	material, err := (sourceRefManagedPostgresProvider{}).IssueCredentials(context.Background(), managedpostgres.CredentialRequest{Access: managedpostgres.CredentialReadWrite, IdentityKey: "staged"})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sink.SealCredential(context.Background(), binding, material)
	if err != nil {
		t.Fatal(err)
	}
	if err = sink.VerifyCredential(context.Background(), binding, sealed, target); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"ref", "kid", "hash", "ciphertext", "key", "generation", "target"} {
		t.Run(mode, func(t *testing.T) {
			b, c, d := binding, sealed, target
			switch mode {
			case "ref":
				c.Ref = "other"
			case "kid":
				c.Kid = "missing"
			case "hash":
				c.ValueHash = "bad"
			case "ciphertext":
				c.Ciphertext = []byte("broken")
			case "key":
				b.EnvironmentKey = "OTHER_URL"
			case "generation":
				b.CredentialGeneration++
			case "target":
				d.ID = "other"
			}
			if err := sink.VerifyCredential(context.Background(), b, c, d); err == nil {
				t.Fatal("invalid envelope verified")
			}
		})
	}
	if calls != 1 {
		t.Fatal("invalid envelope reached SQL probe")
	}
}
