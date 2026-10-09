// adr: 712
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

type entityHTTPEnv struct {
	h     http.Handler
	s     *server
	store *state.MemStore
	key   string
	acct  state.Account
}

func entityAPISetup(t *testing.T) entityHTTPEnv {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "entity-api@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), acct.ID, hash, "entity-test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	s := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	return entityHTTPEnv{h: s.handler(), s: s, store: store, key: key, acct: acct}
}

func (e entityHTTPEnv) do(t *testing.T, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer "+e.key)
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

// This fixture exercises authenticated API admission and the persisted guest
// invocation protocol. It substitutes a trusted HTTP guest and scheduler handoff
// for Firecracker; native workload isolation remains a separate acceptance gate.
type entityDispatchFixture struct {
	*state.MemStore
	t              *testing.T
	guest          http.Handler
	calls          atomic.Int64
	badBody        atomic.Bool
	beforeDispatch func(context.Context, state.Invocation) error
}

func (f *entityDispatchFixture) EnqueueInvocation(ctx context.Context, inv state.Invocation) (state.Invocation, error) {
	queued, err := f.MemStore.EnqueueInvocation(ctx, inv)
	if err != nil {
		return queued, err
	}
	if f.beforeDispatch != nil {
		if err := f.beforeDispatch(ctx, queued); err != nil {
			return queued, err
		}
	}
	claimed, err := f.ClaimInvocation(ctx, queued.ID, "fixture-instance", 60)
	if err != nil {
		return queued, err
	}
	delivered, version, err := state.ResolveInvocationVersion(ctx, f.MemStore, claimed)
	if err != nil {
		return queued, err
	}
	var envelope durableentity.HandlerRequest
	if err := json.Unmarshal(delivered.Payload, &envelope); err != nil || envelope.DeploymentID != version.DeploymentID || envelope.Entity.TenantID != delivered.PlatformTenantID || (delivered.Path != api.DurableEntityHandlerPath && delivered.Path != api.DurableEntityRestoreValidationPath) || delivered.DeadlineAt == nil {
		f.t.Errorf("guest envelope disagrees with authoritative invocation: %+v %v", delivered, err)
	}
	f.calls.Add(1)
	r := httptest.NewRequest(delivered.Method, delivered.Path, strings.NewReader(string(delivered.Payload)))
	var headers map[string]string
	if err := json.Unmarshal(delivered.Headers, &headers); err != nil {
		return queued, err
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	if r.Header.Get("Content-Type") != "application/json" {
		f.t.Error("guest transition did not declare JSON content type")
	}
	rec := httptest.NewRecorder()
	f.guest.ServeHTTP(rec, r)
	if f.badBody.Load() {
		rec.Body.Reset()
		rec.Body.WriteString(`{"data":{},"result":1,"claim_token":"leaked-secret"}`)
	}
	if err := f.CompleteInvocation(ctx, queued.ID, rec.Body.Bytes()); err != nil {
		return queued, err
	}
	return queued, nil
}

type entityTestBucket struct {
	mu      sync.Mutex
	objects map[string]entityTestObject
	rev     int
	lose    atomic.Bool
}

type entityTestObject struct {
	body []byte
	etag string
}

func (b *entityTestBucket) Get(ctx context.Context, key string, maxBytes int64) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	object, exists := b.objects[key]
	if !exists {
		return nil, "", durableentity.ErrNotFound
	}
	if int64(len(object.body)) > maxBytes {
		return nil, "", durableentity.ErrLimit
	}
	return append([]byte(nil), object.body...), object.etag, nil
}

func (b *entityTestBucket) Put(ctx context.Context, key string, body []byte, expected string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	object, exists := b.objects[key]
	if expected == "" && exists || expected != "" && (!exists || object.etag != expected) {
		return "", durableentity.ErrConflict
	}
	b.rev++
	etag := fmt.Sprintf("\"%d\"", b.rev)
	b.objects[key] = entityTestObject{body: append([]byte(nil), body...), etag: etag}
	if strings.HasSuffix(key, "/manifest.json") && strings.Contains(string(body), `"state_version":1`) && b.lose.Swap(false) {
		return "", errors.New("fixture lost response with private-provider-detail")
	}
	return etag, nil
}

func entityCounterGuest(w http.ResponseWriter, r *http.Request) {
	var request durableentity.HandlerRequest
	var state struct {
		Count int `json:"count"`
	}
	var payload struct {
		Delta   int        `json:"delta"`
		AlarmAt *time.Time `json:"alarm_at"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || json.Unmarshal(request.State.Data, &state) != nil || json.Unmarshal(request.Payload, &payload) != nil {
		http.Error(w, "bad envelope", http.StatusBadRequest)
		return
	}
	if request.Event == "alarm" {
		payload.Delta, payload.AlarmAt = 1, nil
	}
	state.Count += payload.Delta
	body, _ := json.Marshal(state)
	_ = json.NewEncoder(w).Encode(durableentity.Transition{Data: body, Result: body, AlarmAt: payload.AlarmAt})
}

func entityAPIFixture(t *testing.T, project bool) (entityHTTPEnv, state.App, *entityDispatchFixture, *entityTestBucket) {
	t.Helper()
	e := entityAPISetup(t)
	projectID := ""
	if project {
		p, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "entity-project"})
		if err != nil {
			t.Fatal(err)
		}
		projectID = p.ID
		for _, slug := range []string{"staging"} {
			if _, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: projectID, Slug: slug}); err != nil {
				t.Fatal(err)
			}
		}
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: projectID, Slug: "entity-counter", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{state.DefaultInvocationDeploymentScope(app), "staging"} {
		if !project && scope == "staging" {
			continue
		}
		dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
			t.Fatal(err)
		}
	}
	bucket := &entityTestBucket{objects: map[string]entityTestObject{}}
	engine, err := durableentity.Open(t.Context(), bucket, durableentity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := &entityDispatchFixture{MemStore: e.store, t: t, guest: http.HandlerFunc(entityCounterGuest)}
	e.s.store, e.s.durableEntities, e.s.durableEntityOwner, e.s.durableEntityApps = dispatch, engine, uuid.NewString(), map[string]bool{app.ID: true}
	return e, app, dispatch, bucket
}

func entityRequest(id string) api.DurableEntityInvokeRequest {
	return api.DurableEntityInvokeRequest{Namespace: "counters", Key: "customer:456", RequestID: id, Payload: json.RawMessage(`{"delta":1}`)}
}

func entityAPIResult(t *testing.T, rec *httptest.ResponseRecorder) api.DurableEntityInvokeResponse {
	t.Helper()
	var result api.DurableEntityInvokeResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
		t.Fatalf("entity invocation: %d %s", rec.Code, rec.Body.String())
	}
	return result
}

func TestDurableEntityAPIConcurrentCallsAndRestartReplay(t *testing.T) {
	e, _, dispatch, bucket := entityAPIFixture(t, false)
	path := "/v1/apps/entity-counter/entities/invoke"
	first := entityAPIResult(t, e.do(t, http.MethodPost, path, entityRequest("first"), nil))
	if string(first.Value) != `{"count":1}` || first.Version != 1 || first.Replayed {
		t.Fatalf("first = %+v", first)
	}
	const calls = 12
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := e.do(t, http.MethodPost, path, entityRequest(fmt.Sprint(i)), nil)
			if rec.Code != http.StatusOK {
				t.Errorf("concurrent call: %d %s", rec.Code, rec.Body.String())
			}
		}()
	}
	wg.Wait()
	restarted, err := durableentity.Open(t.Context(), bucket, durableentity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.s.durableEntities, e.s.durableEntityOwner = restarted, uuid.NewString()
	// Make deployment selection fail: receipts must replay before selecting code.
	e.s.store = &entityNoExecutionStore{MemStore: e.store}
	replayed := entityAPIResult(t, e.do(t, http.MethodPost, path, entityRequest("first"), nil))
	if string(replayed.Value) != string(first.Value) || replayed.Version != first.Version || !replayed.Replayed || dispatch.calls.Load() != calls+1 {
		t.Fatalf("restart replay = %+v, handler calls = %d", replayed, dispatch.calls.Load())
	}
}

type entityNoExecutionStore struct{ *state.MemStore }

func (*entityNoExecutionStore) LiveDeploymentForScope(context.Context, string, string) (state.Deployment, error) {
	return state.Deployment{}, state.ErrNotFound
}

func TestDurableEntityAPIScopeAndAuthentication(t *testing.T) {
	e, _, dispatch, _ := entityAPIFixture(t, true)
	path := "/v1/apps/entity-counter/entities/invoke"
	one, _, err := e.store.CreatePlatformTenant(t.Context(), e.acct.ID, "one", "One", 10)
	if err != nil {
		t.Fatal(err)
	}
	two, _, err := e.store.CreatePlatformTenant(t.Context(), e.acct.ID, "two", "Two", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ env, tenant string }{{"production", one.ID}, {"staging", one.ID}, {"production", two.ID}, {"production", ""}} {
		req := entityRequest("same")
		req.Environment, req.PlatformTenantID = scope.env, scope.tenant
		res := entityAPIResult(t, e.do(t, http.MethodPost, path, req, map[string]string{"X-Gregale-Platform-Tenant-Id": two.ID, "X-Gregale-Revision": uuid.NewString()}))
		if res.Version != 1 || res.Replayed {
			t.Fatalf("scope aliased another entity: %+v", res)
		}
	}
	for _, tc := range []struct {
		name, key, tenant string
		status            int
	}{{"anonymous", "", "", http.StatusUnauthorized}, {"foreign customer", e.key, uuid.NewString(), http.StatusNotFound}} {
		req := entityRequest("rejected")
		req.PlatformTenantID = tc.tenant
		res := e.do(t, http.MethodPost, path, req, map[string]string{"Authorization": "Bearer " + tc.key})
		if res.Code != tc.status {
			t.Fatalf("%s = %d %s", tc.name, res.Code, res.Body.String())
		}
	}
	if dispatch.calls.Load() != 4 {
		t.Fatal("authentication failure reached guest")
	}
}

func TestDurableEntityAPIUncertainResponseAndInvalidGuest(t *testing.T) {
	e, _, dispatch, bucket := entityAPIFixture(t, false)
	path := "/v1/apps/entity-counter/entities/invoke"
	bucket.lose.Store(true)
	res := e.do(t, http.MethodPost, path, entityRequest("lost"), nil)
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), "durable_entity_outcome_uncertain") || strings.Contains(res.Body.String(), "private-provider-detail") {
		t.Fatalf("uncertain response = %d %s", res.Code, res.Body.String())
	}
	replayed := entityAPIResult(t, e.do(t, http.MethodPost, path, entityRequest("lost"), nil))
	if !replayed.Replayed || dispatch.calls.Load() != 1 {
		t.Fatalf("lost response executed again: %+v", replayed)
	}
	conflict := entityRequest("lost")
	conflict.Payload = json.RawMessage(`{"delta":2}`)
	if res := e.do(t, http.MethodPost, path, conflict, nil); res.Code != http.StatusConflict {
		t.Fatalf("payload conflict = %d %s", res.Code, res.Body.String())
	}
	dispatch.badBody.Store(true)
	if res := e.do(t, http.MethodPost, path, entityRequest("bad"), nil); res.Code != http.StatusUnprocessableEntity || strings.Contains(res.Body.String(), "leaked-secret") {
		t.Fatalf("invalid guest = %d %s", res.Code, res.Body.String())
	}
	dispatch.badBody.Store(false)
	valid := entityAPIResult(t, e.do(t, http.MethodPost, path, entityRequest("bad"), nil))
	if valid.Version != 2 || string(valid.Value) != `{"count":2}` {
		t.Fatalf("bad transition changed state: %+v", valid)
	}
}

func TestDurableEntityAPIRechecksAuthorizationOnReplay(t *testing.T) {
	e, _, dispatch, _ := entityAPIFixture(t, true)
	path := "/v1/apps/entity-counter/entities/invoke"
	tenant, _, err := e.store.CreatePlatformTenant(t.Context(), e.acct.ID, "one", "One", 10)
	if err != nil {
		t.Fatal(err)
	}
	req := entityRequest("original")
	req.PlatformTenantID = tenant.ID
	entityAPIResult(t, e.do(t, http.MethodPost, path, req, nil))
	if _, err := e.store.SetPlatformTenantStatus(t.Context(), e.acct.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if res := e.do(t, http.MethodPost, path, req, nil); res.Code != http.StatusForbidden {
		t.Fatalf("suspended replay = %d %s", res.Code, res.Body.String())
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	if res := e.do(t, http.MethodPost, path, entityRequest("original"), map[string]string{"Authorization": "Bearer " + key}); res.Code != http.StatusForbidden {
		t.Fatalf("read-only invocation = %d %s", res.Code, res.Body.String())
	}
	foreign, err := e.store.CreateAccount(t.Context(), "foreign@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignKey, foreignHash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), foreign.ID, foreignHash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	if res := e.do(t, http.MethodPost, path, req, map[string]string{"Authorization": "Bearer " + foreignKey}); res.Code != http.StatusNotFound {
		t.Fatalf("foreign app invocation = %d %s", res.Code, res.Body.String())
	}
	if dispatch.calls.Load() != 1 {
		t.Fatal("failed authorization reached guest")
	}
}

func TestDurableEntityAPIKeepsCapturedProjectRelease(t *testing.T) {
	e, app, dispatch, _ := entityAPIFixture(t, true)
	dep, err := e.store.LiveDeploymentForScope(t.Context(), app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	members := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}}
	original, err := e.store.PublishProjectReleaseSet(t.Context(), e.acct.ID, app.ProjectID, "staging", 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.beforeDispatch = func(ctx context.Context, queued state.Invocation) error {
		var headers map[string]string
		if err := json.Unmarshal(queued.Headers, &headers); err != nil {
			return err
		}
		if headers[api.ReleaseHeader] != original.ID {
			return errors.New("entity did not capture the selected project release")
		}
		_, err := e.store.PublishProjectReleaseSet(ctx, e.acct.ID, app.ProjectID, "staging", 1800, members)
		return err
	}
	req := entityRequest("stage-release")
	req.Environment = "staging"
	entityAPIResult(t, e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", req, nil))
	rows, err := e.store.ListInvocationsForApp(t.Context(), app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("captured invocation = %d %v", len(rows), err)
	}
	_, delivered, err := state.ResolveInvocationVersion(t.Context(), e.store, rows[0])
	if err != nil || delivered.ReleaseID != original.ID || delivered.DeploymentID != dep.ID {
		t.Fatalf("captured graph changed = %+v %v", delivered, err)
	}
}

func TestDurableEntityPreviewDisabledAndConfigValidation(t *testing.T) {
	e := entityAPISetup(t)
	if _, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "disabled", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}
	res := e.do(t, http.MethodPost, "/v1/apps/disabled/entities/invoke", entityRequest("one"), nil)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled = %d", res.Code)
	}
	for _, raw := range []string{"", "*", "not-an-id", uuid.Nil.String()} {
		if _, err := durableEntityAppAllowlist(raw); err == nil {
			t.Fatalf("accepted preview allowlist %q", raw)
		}
	}
	if err := e.s.configureDurableEntities(t.Context(), func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
}
