package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRedisCacheCreateBindDelete(t *testing.T) {
	e := setupSecrets(t, api.PlanPro)
	ctx := context.Background()

	rec := e.do(t, "POST", "/v1/redis", api.CreateRedisCacheRequest{Name: "sessions", MemoryMB: 256}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var cache api.RedisCache
	if err := json.Unmarshal(rec.Body.Bytes(), &cache); err != nil {
		t.Fatal(err)
	}
	if cache.Name != "sessions" || cache.MemoryMB != 256 || cache.EvictionPolicy != "allkeys-lru" ||
		cache.Host != "sessions.svc.gregale" || cache.Port != 6379 || cache.Engine != "valkey" {
		t.Fatalf("cache = %+v", cache)
	}

	app, err := e.store.AppBySlug(ctx, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if app.Visibility != api.AppVisibilityInternal || app.RAMMB != 256+api.ManagedValkeyVMOverheadMB || app.MinInstances != 0 {
		t.Fatalf("app = visibility %s ram %d min %d", app.Visibility, app.RAMMB, app.MinInstances)
	}
	if !slices.ContainsFunc(app.Manifest.Ports, func(p api.WorkloadPort) bool { return p.Port == 6379 && p.Internal }) {
		t.Fatalf("ports = %+v", app.Manifest.Ports)
	}
	if app.ScalingPolicy == nil || app.ScalingPolicy.MaxInstances != 1 {
		t.Fatalf("scaling policy = %+v, want max 1 instance", app.ScalingPolicy)
	}
	dep, err := e.store.LatestDeployment(ctx, app.ID)
	if err != nil {
		t.Fatalf("deployment: %v", err)
	}
	if dep.ImageDigest != api.ManagedValkeyImage {
		t.Fatalf("image = %s", dep.ImageDigest)
	}

	list := e.do(t, "GET", "/v1/redis", nil, nil)
	var listed api.ListRedisCachesResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || list.Code != 200 || len(listed.Caches) != 1 || listed.Limit != 3 {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}

	web := createApp(t, e, "web")
	bind := e.do(t, "POST", "/v1/redis/sessions/bindings", api.BindRedisCacheRequest{App: "web"}, nil)
	if bind.Code != http.StatusOK {
		t.Fatalf("bind: %d %s", bind.Code, bind.Body.String())
	}
	if secret, err := e.store.GetAppSecretInScope(ctx, e.acct.ID, web.ID, api.DefaultEnvScope, "REDIS_URL"); err != nil || secret == nil {
		t.Fatalf("REDIS_URL secret on web: %v", err)
	}
	// Binding grants the call (ADR-20261124174500); unbind takes it back.
	if granted, err := e.store.ManagedValkeyCacheGranted(ctx, app.ID, web.ID); err != nil || !granted {
		t.Fatalf("grant after bind = %v, %v; want true", granted, err)
	}
	// `gregale redis cli` runs through the bound app; get names it.
	var got api.RedisCache
	if rec := e.do(t, "GET", "/v1/redis/sessions", nil, nil); rec.Code != http.StatusOK ||
		json.Unmarshal(rec.Body.Bytes(), &got) != nil || !slices.Equal(got.BoundApps, []string{"web"}) {
		t.Fatalf("get after bind: %d %s, want bound_apps [web]", rec.Code, rec.Body.String())
	}
	// Prod hunt r50: deleting a bound cache left web's REDIS_URL pointing at
	// a deleted cache; it is refused with the unbind step instead.
	if bound := e.do(t, "DELETE", "/v1/redis/sessions", nil, nil); bound.Code != http.StatusConflict ||
		!strings.Contains(bound.Body.String(), "redis_cache_bound") ||
		!strings.Contains(bound.Body.String(), "gregale redis unbind sessions --app web") {
		t.Fatalf("delete while bound: %d %s, want 409 naming the unbind", bound.Code, bound.Body.String())
	}
	if unbind := e.do(t, "DELETE", "/v1/redis/sessions/bindings/web", nil, nil); unbind.Code != http.StatusOK ||
		!strings.Contains(unbind.Body.String(), `"app":"web"`) {
		t.Fatalf("unbind: %d %s", unbind.Code, unbind.Body.String())
	}
	if granted, err := e.store.ManagedValkeyCacheGranted(ctx, app.ID, web.ID); err != nil || granted {
		t.Fatalf("grant after unbind = %v, %v; want false", granted, err)
	}
	if again := e.do(t, "DELETE", "/v1/redis/sessions/bindings/web", nil, nil); again.Code != http.StatusNotFound {
		t.Fatalf("second unbind = %d %s, want 404", again.Code, again.Body.String())
	}
	if notCache := e.do(t, "GET", "/v1/redis/web", nil, nil); notCache.Code != http.StatusNotFound {
		t.Fatalf("get of a plain app = %d, want 404", notCache.Code)
	}

	del := e.do(t, "DELETE", "/v1/redis/sessions", nil, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", del.Code, del.Body.String())
	}
	after := e.do(t, "GET", "/v1/redis", nil, nil)
	if !strings.Contains(after.Body.String(), `"caches":[]`) {
		t.Fatalf("list after delete: %s", after.Body.String())
	}
}

func TestRedisCacheRestartKeepsSnapshots(t *testing.T) {
	e := setupSecrets(t, api.PlanPro)
	if rec := e.do(t, "POST", "/v1/redis", api.CreateRedisCacheRequest{Name: "sessions"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	app, err := e.store.AppBySlug(context.Background(), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if app.Status != state.AppActive {
		if _, err := e.store.CompareAndSetAppStatus(context.Background(), app.ID, app.Status, state.AppActive); err != nil {
			t.Fatalf("activate: %v", err)
		}
	}
	// ?fresh=true would discard snapshots; a cache restart ignores it.
	rec := e.do(t, "POST", "/v1/redis/sessions/restart?fresh=true", nil, map[string]string{"Idempotency-Key": "restart-1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("restart: %d %s", rec.Code, rec.Body.String())
	}
	var out api.AppRestartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.WakeID == "" {
		t.Fatalf("restart response: %s", rec.Body.String())
	}
	if notCache := e.do(t, "POST", "/v1/redis/nope/restart", nil, map[string]string{"Idempotency-Key": "restart-2"}); notCache.Code != http.StatusNotFound {
		t.Fatalf("restart of a missing cache = %d", notCache.Code)
	}

	// A parked cache, or one between VMs during a resize, has nothing to
	// restart. The refusal speaks of the cache and never points at the
	// backing app with `gregale wake`.
	if _, err := e.store.CompareAndSetAppStatus(context.Background(), app.ID, state.AppActive, state.AppEvictedCold); err != nil {
		t.Fatalf("park: %v", err)
	}
	parked := e.do(t, "POST", "/v1/redis/sessions/restart", nil, map[string]string{"Idempotency-Key": "restart-3"})
	var problem api.Problem
	if err := json.Unmarshal(parked.Body.Bytes(), &problem); err != nil || parked.Code != http.StatusConflict ||
		problem.Title != "Redis cache is not running" || strings.Contains(problem.Hint, "gregale wake") ||
		!strings.Contains(problem.Hint, "gregale redis get sessions") {
		t.Fatalf("restart of a parked cache = %d %s", parked.Code, parked.Body.String())
	}
}

func TestRedisCacheResizeAndUpgradeHandOff(t *testing.T) {
	e := setupSecrets(t, api.PlanPro)
	ctx := context.Background()
	if rec := e.do(t, "POST", "/v1/redis", api.CreateRedisCacheRequest{Name: "sessions", MemoryMB: 256}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	app, _ := e.store.AppBySlug(ctx, "sessions")
	// The handoff copies from a running VM, so a resize first wakes the
	// cache; here the cache is already running.
	created, err := e.store.LatestDeployment(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateDeploymentStatus(ctx, created.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateInstanceWithMode(ctx, app.ID, created.ID, string(state.StateRunning), app.RAMMB, "", "wake-1", string(state.InstanceModeNormal)); err != nil {
		t.Fatal(err)
	}
	// Every refusal is logged by the cache's app ID and route, so one a
	// customer reports can be traced (one in the 2026-11-24 trial was not).
	var logs bytes.Buffer
	e.s.log = slog.New(slog.NewJSONHandler(&logs, nil))

	if rec := e.do(t, "PATCH", "/v1/redis/sessions", api.UpdateRedisCacheRequest{MemoryMB: 128}, map[string]string{"Idempotency-Key": "shrink"}); rec.Code != http.StatusConflict {
		t.Fatalf("shrink without evict: %d %s", rec.Code, rec.Body.String())
	} else {
		// The refusal names the CLI flag that allows the shrink.
		var problem api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Code != "cache_shrink_needs_evict" ||
			!strings.Contains(problem.Hint, "gregale redis resize sessions --memory 128 --evict") {
			t.Fatalf("shrink refusal = %s", rec.Body.String())
		}
	}
	// A shrink that also changes the policy keeps the policy in the hint.
	if rec := e.do(t, "PATCH", "/v1/redis/sessions", api.UpdateRedisCacheRequest{MemoryMB: 128, EvictionPolicy: "allkeys-lfu"}, map[string]string{"Idempotency-Key": "shrink-policy"}); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "--memory 128 --eviction-policy allkeys-lfu --evict") {
		t.Fatalf("shrink with policy refusal: %d %s", rec.Code, rec.Body.String())
	}
	rec := e.do(t, "PATCH", "/v1/redis/sessions", api.UpdateRedisCacheRequest{MemoryMB: 512}, map[string]string{"Idempotency-Key": "grow"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("grow: %d %s", rec.Code, rec.Body.String())
	}
	var change api.RedisCacheChange
	if err := json.Unmarshal(rec.Body.Bytes(), &change); err != nil || change.Cache.MemoryMB != 512 || change.DeploymentID == "" {
		t.Fatalf("change = %s", rec.Body.String())
	}
	app, _ = e.store.AppBySlug(ctx, "sessions")
	if app.RAMMB != 512+api.ManagedValkeyVMOverheadMB {
		t.Fatalf("app RAM = %d", app.RAMMB)
	}
	dep, err := e.store.LatestDeployment(ctx, app.ID)
	if err != nil || dep.ImageDigest != api.ManagedValkeyImage {
		t.Fatalf("handoff deployment: %+v %v", dep, err)
	}

	up := e.do(t, "POST", "/v1/redis/sessions/upgrade", nil, map[string]string{"Idempotency-Key": "upgrade"})
	if up.Code != http.StatusAccepted {
		t.Fatalf("upgrade: %d %s", up.Code, up.Body.String())
	}
	if rec := e.do(t, "PATCH", "/v1/redis/sessions", api.UpdateRedisCacheRequest{}, map[string]string{"Idempotency-Key": "noop"}); rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty change: %d", rec.Code)
	}
	var refused []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) == nil && entry["msg"] == "apid: redis request refused" {
			refused = append(refused, entry)
		}
	}
	codes := map[string]bool{}
	for _, entry := range refused {
		if entry["app_id"] != app.ID || entry["route"] == "" {
			t.Fatalf("refusal log lacks app_id or route: %v", entry)
		}
		code, _ := entry["code"].(string)
		codes[code] = true
	}
	if !codes["cache_shrink_needs_evict"] || len(refused) != 3 {
		t.Fatalf("refusal logs = %v, want the two shrinks and the empty change", refused)
	}
}

func TestRedisCachePlanGates(t *testing.T) {
	free := setupSecrets(t, api.PlanFree)
	if rec := free.do(t, "POST", "/v1/redis", api.CreateRedisCacheRequest{Name: "c1"}, nil); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), api.CodeManagedRedisNotInPlan) {
		t.Fatalf("free: %d %s", rec.Code, rec.Body.String())
	}

	hobby := setupSecrets(t, api.PlanHobby)
	for _, tc := range []struct {
		name string
		req  api.CreateRedisCacheRequest
	}{
		{"too big for hobby", api.CreateRedisCacheRequest{Name: "c2", MemoryMB: 512}},
		{"not a step", api.CreateRedisCacheRequest{Name: "c3", MemoryMB: 100}},
		{"bad policy", api.CreateRedisCacheRequest{Name: "c4", EvictionPolicy: "lru"}},
		{"idle too short", api.CreateRedisCacheRequest{Name: "c5", IdleTimeoutS: 30}},
	} {
		if rec := hobby.do(t, "POST", "/v1/redis", tc.req, nil); rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}
	if rec := hobby.do(t, "POST", "/v1/redis", api.CreateRedisCacheRequest{Name: "first"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("first hobby cache: %d %s", rec.Code, rec.Body.String())
	}
	if rec := hobby.do(t, "POST", "/v1/redis", api.CreateRedisCacheRequest{Name: "second"}, nil); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), api.CodeManagedRedisLimit) {
		t.Fatalf("second hobby cache: %d %s", rec.Code, rec.Body.String())
	}
}

var _ state.ManagedValkeyStore = (*state.MemStore)(nil)

// The 2026-11-24 voting-app eval: Compose wrote REDIS_URL on each consumer,
// but the project policy refused every connection to the cache. Compose
// provisioning grants each consumer the call, as `redis bind` does.
func TestComposeRedisGrantsConsumers(t *testing.T) {
	e := setupSecrets(t, api.PlanPro)
	ctx := context.Background()
	if rec := e.do(t, "POST", "/v1/redis", api.CreateRedisCacheRequest{Name: "voting-redis"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	cache, err := e.store.AppBySlug(ctx, "voting-redis")
	if err != nil {
		t.Fatal(err)
	}
	worker := createApp(t, e, "voting-worker")
	worker.WorkloadName = "worker"
	provisions := map[string]composeRedisProvision{"redis": {service: "redis", cache: "voting-redis", existing: true,
		bindings: []composeRedisBinding{{Workload: "worker", EnvKey: "REDIS_URL", Value: api.ManagedValkeyURL("voting-redis")}}}}
	r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	if problem := e.s.applyComposeRedis(r, e.acct, api.DefaultEnvScope, provisions, []state.App{worker}); problem != nil {
		t.Fatalf("applyComposeRedis: %+v", problem)
	}
	if granted, err := e.store.ManagedValkeyCacheGranted(ctx, cache.ID, worker.ID); err != nil || !granted {
		t.Fatalf("consumer grant = %v, %v; want true", granted, err)
	}

	// A planned cache that is gone by apply time is refused, not granted.
	provisions["redis"] = composeRedisProvision{service: "redis", cache: "voting-gone", existing: true,
		bindings: provisions["redis"].bindings}
	if problem := e.s.applyComposeRedis(r, e.acct, api.DefaultEnvScope, provisions, []state.App{worker}); problem == nil ||
		problem.Code != "managed_redis_cache_missing" {
		t.Fatalf("apply with a missing cache = %+v, want managed_redis_cache_missing", problem)
	}
}
