package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
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
	if secret, err := e.store.GetAppSecretInScope(ctx, e.acct.ID, app.ID, api.DefaultEnvScope, api.ManagedValkeyPasswordEnv); err != nil || secret == nil {
		t.Fatalf("password secret: %v", err)
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

func TestDeriveRedisPasswordIsStableAndPerCache(t *testing.T) {
	previous := hostHMACKey
	hostHMACKey = func() []byte { return []byte("0123456789abcdef0123456789abcdef") }
	t.Cleanup(func() { hostHMACKey = previous })
	a1, err := deriveRedisPassword("app-a")
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := deriveRedisPassword("app-a")
	b, _ := deriveRedisPassword("app-b")
	if a1 != a2 || a1 == b || len(a1) != 64 {
		t.Fatalf("a1=%s a2=%s b=%s", a1, a2, b)
	}
	hostHMACKey = func() []byte { return nil }
	if _, err := deriveRedisPassword("app-a"); err == nil {
		t.Fatal("want an error without a host HMAC key")
	}
}

var _ state.ManagedValkeyStore = (*state.MemStore)(nil)
