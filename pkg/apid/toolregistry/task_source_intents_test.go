package toolregistry_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/tasktoolsource/sourcecheck"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type hostedSourceFixture struct {
	store    state.Store
	account  state.Account
	key      state.APIKey
	review   tasktoolsource.Review
	request  api.HostedTaskSourcePrepare
	identity *age.X25519Identity
	executor *toolregistry.PrivateExecutor
}

func newHostedSourceFixture(t *testing.T, store state.Store) *hostedSourceFixture {
	t.Helper()
	ctx := t.Context()
	f := &hostedSourceFixture{store: store}
	var err error
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	f.account, err = store.CreateAccount(ctx, "source-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("source-key:" + suffix))
	f.key, err = store.CreateAPIKey(ctx, f.account.ID, digest[:], "source-agent", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := mcphosting.Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{}, Auth: mcphosting.AuthConfig{Mode: "open", ToolScopes: mcphosting.ScopePolicy{"report": {}}}, Tasks: &mcphosting.TasksConfig{Enabled: &enabled, DatabaseURLEnv: "DATABASE_URL", OwnerKeyEnv: "MCP_TASK_OWNER_KEY", NamespaceEnv: "MCP_TASK_NAMESPACE", TTLSeconds: 3600, WorkerConcurrency: 2}}
	if err = cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(cfg)
	entries := []api.HostedTaskSourceEntry{{Path: ".", Mode: 0700, Directory: true}, {Path: mcphosting.ConfigFile, Mode: 0600, Content: config}, {Path: "package.json", Mode: 0600, Content: []byte(`{"scripts":{"start":"node server.js"}}`)}, {Path: "server.js", Mode: 0600, Content: []byte("SOURCE_SENTINEL_MUST_STAY_PRIVATE")}, {Path: "tasks-release.js", Mode: 0700, Content: []byte{0, 1, 255}}}
	hash, err := tasktoolsource.Digest(entries)
	if err != nil {
		t.Fatal(err)
	}
	f.review = tasktoolsource.Review{TaskReleaseReview: api.TaskReleaseReview{Version: 1, Status: "review_only", Protocol: api.MCPProtocolVersion, WorkerRuntime: "node", Endpoint: cfg.Endpoint, AccessMode: cfg.Auth.Mode, ToolScopes: cfg.Auth.ToolScopes, PreviousWorkerApps: []string{}, ReadinessRoles: []string{}, RequiredSetup: tasktoolrelease.RequiredSetup(), TimeoutSeconds: 300, WebLimits: mcphosting.ReviewTaskLimits(*cfg.Tasks), WorkerLimits: mcphosting.ReviewTaskLimits(*cfg.Tasks), MigrationsRequired: true}, Kind: "hosted_task_source_review", AccountID: f.account.ID, Sources: map[string]string{"web": hash, "worker": hash}}
	for _, role := range []string{"web", "worker", "observer"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: role + "-" + suffix, Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		pin, err := store.(state.ReleaseReviewStore).ReadReleaseReview(ctx, app.ID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		f.review.Apps = append(f.review.Apps, api.TaskToolReleaseApp{Role: role, App: app.Slug, Review: pin})
		switch role {
		case "web":
			f.review.WebApp = app.Slug
		case "worker":
			f.review.WorkerApp = app.Slug
		case "observer":
			f.review.ObserverApp = app.Slug
			f.review.ObserverMetricApp = app.Slug
		}
	}
	f.review.Fingerprint = tasktoolsource.Fingerprint(f.review)
	raw, _ := json.Marshal(f.review)
	f.request = api.HostedTaskSourcePrepare{Review: raw, Sources: map[string][]api.HostedTaskSourceEntry{"web": entries, "worker": entries}}
	if _, err = sourcecheck.ValidatePrepare(f.account.ID, f.request); err != nil {
		t.Fatal(err)
	}
	f.identity, err = age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	f.executor = toolregistry.NewPrivateExecutor(store, toolregistry.PrivateExecutionOptions{SourceRetentionEnabled: true, ConnectionRecipients: func() ([]*age.X25519Recipient, error) { return []*age.X25519Recipient{f.identity.Recipient()}, nil }})
	t.Cleanup(f.executor.Close)
	return f
}
func (f *hostedSourceFixture) call(t *testing.T, method, id string, in any, key *state.APIKey) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(in)
	req := httptest.NewRequestWithContext(t.Context(), method, "/v1/task-tool-source-intents/"+id, bytes.NewReader(raw))
	req.SetPathValue("id", id)
	req = req.WithContext(middleware.WithPrincipal(req.Context(), f.account, key, nil))
	w := httptest.NewRecorder()
	f.executor.HostedTaskSourceIntent()(w, req, f.account)
	return w
}
func TestHostedTaskSourceNativeMemory(t *testing.T) {
	exerciseHostedSources(t, state.NewMemStore(), nil)
}
func TestHostedTaskSourceNativePostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	exerciseHostedSources(t, state.NewPgStore(pool), pool)
}
func exerciseHostedSources(t *testing.T, store state.Store, pool *pgxpool.Pool) {
	f := newHostedSourceFixture(t, store)
	id := toolpublication.Digest([]byte(f.account.ID))
	ctx := t.Context()
	response := f.call(t, http.MethodPut, id, f.request, &f.key)
	if response.Code != 200 {
		t.Fatalf("prepare %d: %s", response.Code, response.Body.String())
	}
	var record api.HostedTaskSourceIntent
	if json.Unmarshal(response.Body.Bytes(), &record) != nil || tasktoolsource.ValidateRecord(record, f.account.ID, id) != nil {
		t.Fatalf("bad record %s", response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("SOURCE_SENTINEL")) || bytes.Contains(response.Body.Bytes(), []byte("ciphertext")) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("source exposed")
	}
	journal := store.(state.HostedTaskSourceIntentStore)
	private, err := journal.GetHostedTaskSourceIntent(ctx, f.account.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	ns, plain, err := secretbox.OpenBytes(f.identity, private.Ciphertext)
	if err != nil || ns != "gregale-hosted-task-source:"+f.account.ID+":"+f.key.ID+":"+id+":"+f.review.Fingerprint {
		t.Fatalf("seal binding: %q %v", ns, err)
	}
	var decoded map[string][]api.HostedTaskSourceEntry
	if json.Unmarshal(plain, &decoded) != nil || len(decoded["web"]) != 5 || string(decoded["web"][3].Content) != "SOURCE_SENTINEL_MUST_STAY_PRIVATE" {
		t.Fatal("sealed source did not preserve exact bytes")
	}
	private.Ciphertext[0] ^= 255
	private.Record.Review[0] = '!'
	fresh, err := journal.GetHostedTaskSourceIntent(ctx, f.account.ID, id)
	if err != nil || tasktoolsource.ValidateRecord(fresh.Record, f.account.ID, id) != nil {
		t.Fatal("caller mutated journal")
	}
	if _, _, err = secretbox.OpenBytes(f.identity, fresh.Ciphertext); err != nil {
		t.Fatal("caller mutated ciphertext")
	}
	// JSONB property order differs from marshalled Go DTO order: semantic replay
	// must preserve the original receipt and expiry rather than reject it.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := journal.PrepareHostedTaskSourceIntent(ctx, f.account.ID, f.key.ID, id, f.review, []byte("ignored-replay-ciphertext"))
			if err != nil {
				errs <- err
				return
			}
			if got.Record.IntentSHA256 != record.IntentSHA256 || !got.Record.ExpiresAt.Equal(record.ExpiresAt) {
				errs <- errors.New("retry changed identity or expiry")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	changed := f.review
	changed.Apps = append([]api.TaskToolReleaseApp(nil), f.review.Apps...)
	changed.Apps[0].Review.ConfigurationHash = strings.Repeat("f", 64)
	changed.Fingerprint = tasktoolsource.Fingerprint(changed)
	if _, err = journal.PrepareHostedTaskSourceIntent(ctx, f.account.ID, f.key.ID, id, changed, []byte("x")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed id: %v", err)
	}
	if _, err = journal.PrepareHostedTaskSourceIntent(ctx, f.account.ID, f.key.ID, strings.Repeat("1", 64), changed, []byte("x")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale native pin: %v", err)
	}
	keyhash := sha256.Sum256([]byte("other:" + f.account.ID))
	other, err := store.CreateAPIKey(ctx, f.account.ID, keyhash[:], "other", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite})
	if err != nil {
		t.Fatal(err)
	}
	if w := f.call(t, http.MethodGet, id, nil, &other); w.Code != 404 {
		t.Fatalf("other requester read %d", w.Code)
	}
	if _, err = journal.PrepareHostedTaskSourceIntent(ctx, f.account.ID, other.ID, id, f.review, []byte("x")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("other requester replay: %v", err)
	}
	if w := f.call(t, http.MethodGet, id, nil, nil); w.Code != 200 {
		t.Fatalf("owner metadata read %d", w.Code)
	}
	var admitted atomic.Int32
	var quotaWG sync.WaitGroup
	quotaErrs := make(chan error, 4)
	for i := range 4 {
		quotaWG.Add(1)
		go func() {
			defer quotaWG.Done()
			_, err := journal.PrepareHostedTaskSourceIntent(ctx, f.account.ID, f.key.ID, toolpublication.Digest([]byte(fmt.Sprintf("quota:%s:%d", f.account.ID, i))), f.review, []byte("second"))
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, state.ErrHostedTaskSourceLimit) {
				quotaErrs <- err
			}
		}()
	}
	quotaWG.Wait()
	close(quotaErrs)
	for err := range quotaErrs {
		t.Error(err)
	}
	if admitted.Load() != 1 {
		t.Fatalf("concurrent quota admitted %d additional sources", admitted.Load())
	}

	if _, err = journal.PrepareHostedTaskSourceIntent(ctx, f.account.ID, f.key.ID, strings.Repeat("3", 64), f.review, []byte("third")); !errors.Is(err, state.ErrHostedTaskSourceLimit) {
		t.Fatalf("active quota %v", err)
	}
	if pool != nil {
		expires := time.Now().UTC().Add(-time.Minute)
		created := expires.Add(-api.HostedTaskSourceRetention)
		_, err = pool.Exec(ctx, `UPDATE hosted_task_source_intents SET expires_at=$3,payload=jsonb_set(jsonb_set(payload,'{created_at}',to_jsonb($4::timestamptz)),'{expires_at}',to_jsonb($3::timestamptz)) WHERE account_id=$1::uuid AND id=$2`, f.account.ID, id, expires, created)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := journal.PurgeExpiredHostedTaskSources(ctx); err != nil || n != 1 {
			t.Fatalf("expiry cleanup %d %v", n, err)
		}
		got, err := journal.GetHostedTaskSourceIntent(ctx, f.account.ID, id)
		if err != nil || len(got.Ciphertext) != 0 || got.Record.SourceState != "expired" {
			t.Fatalf("expired record: %+v %v", got.Record, err)
		}
		again, err := journal.PrepareHostedTaskSourceIntent(ctx, f.account.ID, f.key.ID, id, f.review, []byte("no renewal"))
		if err != nil || len(again.Ciphertext) != 0 || again.Record.SourceState != "expired" || !again.Record.ExpiresAt.Equal(got.Record.ExpiresAt) {
			t.Fatalf("expired replay renewed: %v", err)
		}
		// Expiry releases bytes but never makes the bounded identity ledger
		// reusable. Populate genuine expired metadata to exercise that limit.
		for i := 0; i < api.HostedTaskSourceRecordsPerAccount-2; i++ {
			tombID := toolpublication.Digest([]byte(fmt.Sprintf("tomb:%s:%d", f.account.ID, i)))
			tomb, err := tasktoolsource.NewRecord(f.account.ID, f.key.ID, tombID, f.review, created)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(tomb)
			if _, err = pool.Exec(ctx, `INSERT INTO hosted_task_source_intents(account_id,id,payload,expires_at) VALUES($1::uuid,$2,$3::jsonb,$4)`, f.account.ID, tombID, payload, tomb.ExpiresAt); err != nil {
				t.Fatal(err)
			}
		}
		_, err = journal.PrepareHostedTaskSourceIntent(ctx, f.account.ID, f.key.ID, strings.Repeat("4", 64), f.review, []byte("new source"))
		var quota *state.HostedTaskSourceLimitError
		if !errors.As(err, &quota) || quota.Quota != "identities" || quota.Limit != 256 || quota.Observed != 257 {
			t.Fatalf("expired identity cap: %v", err)
		}
		// Restart constructs a fresh store and handler over the same database.
		restarted := state.NewPgStore(pool)
		persisted, err := restarted.GetHostedTaskSourceIntent(ctx, f.account.ID, id)
		if err != nil || persisted.Record.IntentSHA256 != record.IntentSHA256 {
			t.Fatal("restart lost intent")
		}
	}
	f.executor.StartSourceRetentionCleanup()
	f.executor.StartSourceRetentionCleanup()
	f.executor.Close()
}
func TestHostedTaskSourceNativeFailClosed(t *testing.T) {
	f := newHostedSourceFixture(t, state.NewMemStore())
	id := strings.Repeat("a", 64)
	for name, opts := range map[string]toolregistry.PrivateExecutionOptions{"disabled": {}, "no-account-seal": {SourceRetentionEnabled: true}} {
		t.Run(name, func(t *testing.T) {
			old := f.executor
			f.executor = toolregistry.NewPrivateExecutor(f.store, opts)
			defer func() { f.executor.Close(); f.executor = old }()
			if w := f.call(t, http.MethodPut, id, f.request, &f.key); w.Code != 503 {
				t.Fatalf("unavailable %d", w.Code)
			}
		})
	}
	bad := f.request
	bad.Review = append(json.RawMessage(nil), f.request.Review...)
	var fields map[string]any
	_ = json.Unmarshal(bad.Review, &fields)
	fields["ready_to_apply"] = true
	bad.Review, _ = json.Marshal(fields)
	if w := f.call(t, http.MethodPut, id, bad, &f.key); w.Code < 400 {
		t.Fatal("authority-bearing review accepted")
	}
	bad = f.request
	bad.Sources = map[string][]api.HostedTaskSourceEntry{"web": append([]api.HostedTaskSourceEntry(nil), f.request.Sources["web"]...), "worker": f.request.Sources["worker"]}
	bad.Sources["web"][0].Path = "../outside"
	if w := f.call(t, http.MethodPut, id, bad, &f.key); w.Code < 400 {
		t.Fatal("unsafe source accepted")
	}
	if _, err := f.store.(state.HostedTaskSourceIntentStore).GetHostedTaskSourceIntent(context.Background(), f.account.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("partial intent persisted")
	}
	if w := f.call(t, http.MethodPut, id, f.request, &f.key); w.Code != 200 {
		t.Fatal("valid prepare failed", w.Code, w.Body.String())
	}
	if _, err := f.store.MarkAPIKeyRevoked(t.Context(), f.account.ID, f.key.ID); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		if w := f.call(t, method, id, f.request, &f.key); w.Code != 404 {
			t.Fatalf("cached revoked key accepted by %s: %d", method, w.Code)
		}
	}
}
