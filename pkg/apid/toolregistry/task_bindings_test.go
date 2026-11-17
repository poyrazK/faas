package toolregistry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestHostedTaskBindingsMemory(t *testing.T) {
	exerciseHostedTaskBindings(t, state.NewMemStore(), nil)
}
func TestHostedTaskBindingsPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	exerciseHostedTaskBindings(t, state.NewPgStore(pool), pool)
}
func exerciseHostedTaskBindings(t *testing.T, store state.Store, pool *pgxpool.Pool) {
	t.Helper()
	f := newHostedSourceFixture(t, store)
	runtimeID, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	databaseID := uuid.NewString()
	if pool != nil {
		_, err = pool.Exec(t.Context(), `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint,state,provider_resource_id,observed_generation) VALUES($1,$2,'binding-test','eu',17,'development','single_zone','test',$3,'ready','binding-test',1)`, databaseID, f.account.ID, strings.Repeat("a", 64))
		if err != nil {
			t.Fatal(err)
		}
	}
	put := func(app, key, value string, managed bool, access string) {
		t.Helper()
		cipher, err := secretbox.SealOneBound(runtimeID.Recipient(), secretbox.Binding{AccountID: f.account.ID}, key, value, api.HostedTaskBindingSecretMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		if !managed {
			err = store.UpsertAppSecret(t.Context(), f.account.ID, app, key, cipher)
		} else {
			bindingID := uuid.NewString()
			ref := "binding:" + bindingID
			if pool != nil {
				_, err = pool.Exec(t.Context(), `INSERT INTO managed_postgres_bindings(id,account_id,database_account_id,database_id,app_id,scope,environment_key,provider_identity_id,credential_ref,state,access) VALUES($1,$2,$2,$3,$4,'default',$5,'test',$6,'ready',$7)`, bindingID, f.account.ID, databaseID, app, key, ref, access)
				if err != nil {
					t.Fatal(err)
				}
			}
			sink := store.(interface {
				PutManagedPostgresSecret(context.Context, state.AppSecret) error
			})
			err = sink.PutManagedPostgresSecret(t.Context(), state.AppSecret{AccountID: f.account.ID, AppID: app, Scope: api.DefaultEnvScope, Key: key, Ciphertext: cipher, ManagedPostgresBindingID: bindingID, ManagedPostgresDatabaseID: databaseID, ManagedCredentialRef: ref, ManagedCredentialGeneration: 1, ManagedPostgresAccess: access})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, a := range f.review.Apps {
		if err := store.UpsertAppEnv(t.Context(), f.account.ID, a.Review.AppID, "MCP_TASK_NAMESPACE", "binding-test"); err != nil {
			t.Fatal(err)
		}
		access := "read_write"
		if a.Role == "observer" {
			access = "read_only"
		}
		put(a.Review.AppID, "DATABASE_URL", "postgres://binding_role:private-sentinel@db.example.com/task?sslmode=verify-full", true, access)
		if a.Role != "observer" {
			put(a.Review.AppID, "MCP_TASK_OWNER_KEY", strings.Repeat("private-owner-sentinel", 2), false, "")
		}
		if a.Role == "worker" {
			put(a.Review.AppID, "MIGRATION_URL", "postgres://migration:private-sentinel@db.example.com/task?sslmode=verify-full", true, "migration")
		}
		pin, err := store.(state.ReleaseReviewStore).ReadReleaseReview(t.Context(), a.Review.AppID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		f.review.Apps[i].Review = pin
	}
	f.review.Fingerprint = tasktoolsource.Fingerprint(f.review)
	f.request.Review, _ = json.Marshal(f.review)
	var connects atomic.Int32
	opts := toolregistry.PrivateExecutionOptions{SourceRetentionEnabled: true, BindingVerificationEnabled: true, ConnectionRecipients: func() ([]*age.X25519Recipient, error) { return []*age.X25519Recipient{f.identity.Recipient()}, nil }, ConnectionIdentities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{f.identity} }, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{runtimeID} }, BindingProbeConnect: func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
		connects.Add(1)
		return nil, errors.New("private-driver-sentinel")
	}}
	f.executor = toolregistry.NewPrivateExecutor(store, opts)
	t.Cleanup(f.executor.Close)
	id := toolpublication.Digest([]byte(f.account.ID + ":bindings"))
	w := f.call(t, "PUT", id, f.request, &f.key)
	var source api.HostedTaskSourceIntent
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &source) != nil {
		t.Fatalf("prepare: %d %s", w.Code, w.Body)
	}
	nativeSource := store.(state.HostedTaskSourceIntentStore)
	approved, err := nativeSource.ChangeHostedTaskSourceIntent(t.Context(), f.account.ID, id, "decide", api.HostedTaskSourceDecision{IntentSHA256: source.IntentSHA256, ReviewSHA256: source.ReviewSHA256, Decision: "approve"}, state.HostedTaskSourceOwner{Session: true})
	if err != nil || approved.Record.ReviewStatus != "approved" {
		t.Fatalf("approve: %v", err)
	}
	call := func(method, suffix string, in any, key *state.APIKey) *httptest.ResponseRecorder {
		body, _ := json.Marshal(in)
		r := httptest.NewRequestWithContext(t.Context(), method, "/v1/task-tool-source-intents/"+id+"/bindings"+suffix, bytes.NewReader(body))
		r.SetPathValue("id", id)
		r = r.WithContext(middleware.WithPrincipal(r.Context(), f.account, key, nil))
		w := httptest.NewRecorder()
		f.executor.HostedTaskBindings()(w, r, f.account)
		return w
	}
	decode := func(w *httptest.ResponseRecorder, status string) api.HostedTaskBindingVerification {
		t.Helper()
		var out api.HostedTaskBindingVerification
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || tasktoolbinding.ValidateReceipt(out, f.account.ID, id) != nil || out.Status != status {
			t.Fatalf("want %s: %d %s", status, w.Code, w.Body)
		}
		for _, secret := range []string{"private-", "ciphertext", "DATABASE_URL", "MIGRATION_URL", "db.example.com"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("private binding material exposed", secret)
			}
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cached binding receipt")
		}
		return out
	}
	selection := api.HostedTaskBindingSelection{IntentSHA256: source.IntentSHA256, ReviewSHA256: source.ReviewSHA256, Namespace: "binding-test", WebDatabaseRef: "secret:DATABASE_URL", WorkerDatabaseRef: "secret:DATABASE_URL", MigrationDatabaseRef: "secret:MIGRATION_URL", ObserverDatabaseRef: "secret:DATABASE_URL"}
	if w := call("PUT", "", selection, &f.key); w.Code == 200 {
		t.Fatal("requester selected owner bindings")
	}
	selected := decode(call("PUT", "", selection, nil), "selected")
	replay := decode(call("PUT", "", selection, nil), "selected")
	if selected.BindingSHA256 != replay.BindingSHA256 || !selected.ExpiresAt.Equal(source.ExpiresAt) || !selected.CreatedAt.Equal(replay.CreatedAt) {
		t.Fatal("selection replay renewed identity or deadline")
	}
	decode(call("GET", "", nil, &f.key), "selected")
	changed := selection
	changed.Namespace = "other"
	if w := call("PUT", "", changed, nil); w.Code == 200 {
		t.Fatal("changed selection reused immutable identity")
	}
	spoof := map[string]any{"binding_sha256": selected.BindingSHA256, "session": true}
	if w := call("POST", "/check", spoof, nil); w.Code != 400 {
		t.Fatal("accepted caller authority")
	}
	if w := call("POST", "/check", api.HostedTaskBindingCheck{BindingSHA256: strings.Repeat("f", 64)}, nil); w.Code != 409 {
		t.Fatal("accepted wrong binding digest", w.Code)
	}
	check := api.HostedTaskBindingCheck{BindingSHA256: selected.BindingSHA256}
	var wg sync.WaitGroup
	outcomes := make(chan *httptest.ResponseRecorder, 3)
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); outcomes <- call("POST", "/check", check, nil) }()
	}
	wg.Wait()
	close(outcomes)
	for w := range outcomes {
		if w.Code != 200 {
			t.Fatalf("concurrent check: %d %s", w.Code, w.Body)
		}
	}
	decode(call("GET", "", nil, &f.key), "failed")
	if connects.Load() != 1 {
		t.Fatal("repeated probe after concurrent retry", connects.Load())
	}
	// Reopening the executor cannot take over the permanent probe claim.
	f.executor.Close()
	f.executor = toolregistry.NewPrivateExecutor(store, opts)
	t.Cleanup(f.executor.Close)
	decode(call("POST", "/check", check, nil), "failed")
	if connects.Load() != 1 {
		t.Fatal("repeated probe after restart")
	}
	// A failed qualification is still explicitly revocable with admission off.
	f.executor.Close()
	f.executor = toolregistry.NewPrivateExecutor(store, toolregistry.PrivateExecutionOptions{})
	t.Cleanup(f.executor.Close)
	decode(call("POST", "/revoke", check, nil), "revoked")
	decode(call("GET", "", nil, &f.key), "revoked")
	// A second source exercises the final transaction barrier independently of
	// driver behavior: rotation between claim and record must discard success.
	f.executor.Close()
	f.executor = toolregistry.NewPrivateExecutor(store, opts)
	t.Cleanup(f.executor.Close)
	id = toolpublication.Digest([]byte(f.account.ID + ":rotation"))
	prepared := f.call(t, "PUT", id, f.request, &f.key)
	if prepared.Code != 200 || json.Unmarshal(prepared.Body.Bytes(), &source) != nil {
		t.Fatalf("rotation prepare: %d %s", prepared.Code, prepared.Body)
	}
	_, err = nativeSource.ChangeHostedTaskSourceIntent(t.Context(), f.account.ID, id, "decide", api.HostedTaskSourceDecision{IntentSHA256: source.IntentSHA256, ReviewSHA256: source.ReviewSHA256, Decision: "approve"}, state.HostedTaskSourceOwner{Session: true})
	if err != nil {
		t.Fatal(err)
	}
	selection.IntentSHA256, selection.ReviewSHA256 = source.IntentSHA256, source.ReviewSHA256
	selected = decode(call("PUT", "", selection, nil), "selected")
	journal := store.(state.HostedTaskBindingStore)
	change := state.HostedTaskBindingChange{Owner: state.HostedTaskSourceOwner{KeyID: f.key.ID}, BindingSHA256: selected.BindingSHA256, Claim: uuid.NewString()}
	if _, err := journal.ChangeHostedTaskBindings(t.Context(), f.account.ID, id, "claim", change); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("native probe accepted agent owner", err)
	}
	change.Owner = state.HostedTaskSourceOwner{Session: true}
	claimed, err := journal.ChangeHostedTaskBindings(t.Context(), f.account.ID, id, "claim", change)
	if err != nil || claimed.Public.Status != "verifying" {
		t.Fatal("native claim", err)
	}
	webApp := ""
	for _, a := range f.review.Apps {
		if a.Role == "web" {
			webApp = a.Review.AppID
		}
	}
	put(webApp, "MCP_TASK_OWNER_KEY", strings.Repeat("rotated-owner-sentinel", 2), false, "")
	change.Checks = tasktoolbinding.Results("passed")
	completed, err := journal.ChangeHostedTaskBindings(t.Context(), f.account.ID, id, "record", change)
	if err != nil || completed.Public.Status != "stale" || completed.Public.ValidUntil != nil {
		t.Fatalf("rotation published passing evidence: %+v %v", completed.Public, err)
	}
	decode(call("GET", "", nil, &f.key), "stale")
	_, err = nativeSource.ChangeHostedTaskSourceIntent(t.Context(), f.account.ID, id, "decide", api.HostedTaskSourceDecision{IntentSHA256: source.IntentSHA256, ReviewSHA256: source.ReviewSHA256, Decision: "revoke"}, state.HostedTaskSourceOwner{Session: true})
	if err != nil {
		t.Fatal(err)
	}
	decode(call("GET", "", nil, &f.key), "revoked")
	if _, err := journal.ChangeHostedTaskBindings(t.Context(), uuid.NewString(), id, "get", state.HostedTaskBindingChange{}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account qualification", err)
	}

	if pool != nil {
		var count int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM hosted_task_binding_qualifications WHERE account_id=$1", f.account.ID).Scan(&count); err != nil || count != 2 {
			t.Fatal("unbounded binding identities", count, err)
		}
	}
}
