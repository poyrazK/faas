package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEdgeRulesCache_E2E_FreeQuotaRejected(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanFree)
	slug := "cache-free-e2e"
	created, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug, RequireAuthn: boolPtr(false)})
	if status != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", status, created)
	}

	action, err := json.Marshal(api.EdgeRuleCacheAction{
		MaxAgeSeconds:       30,
		StaleIfErrorSeconds: 0,
		Methods:             []string{http.MethodGet},
	})
	if err != nil {
		t.Fatalf("marshal cache action: %v", err)
	}
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/edge-rules",
		api.CreateEdgeRuleRequest{
			MatchHost:    slug + ".apps.test.example",
			MatchPath:    "/products/*",
			MatchMethods: []string{http.MethodGet},
			Kind:         string(state.EdgeRuleKindCache),
			Action:       action,
		})
	if status != http.StatusForbidden {
		t.Fatalf("Free cache rule: status=%d body=%s, want 403", status, raw)
	}
	var problem api.Problem
	if err := json.Unmarshal(raw, &problem); err != nil {
		t.Fatalf("decode problem: %v body=%s", err, raw)
	}
	if problem.Code != api.CodePlanEdgeRuleKindQuotaReached {
		t.Fatalf("problem code=%q, want %q", problem.Code, api.CodePlanEdgeRuleKindQuotaReached)
	}
}

// TestEdgeRulesCache_E2E_DeclarativeCLIAndDistributedPurge covers the complete
// customer path: CLI declaration -> apid validation/persistence -> pg_notify
// convergence -> gateway origin fill -> Redis L2 -> fresh hit -> SWR refresh ->
// CLI purge -> gateway/Redis invalidation. It deliberately uses the real
// subprocess harness rather than importing command packages.
func TestEdgeRulesCache_E2E_DeclarativeCLIAndDistributedPurge(t *testing.T) {
	redis := miniredis.RunT(t)
	f := newNormalPathFixtureWithPlanAndEnv(t, "cache-e2e", api.PlanHobby,
		"FAAS_GATEWAY_RESPONSE_CACHE_REDIS_URL=redis://"+redis.Addr())
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "cache-v1")
	f.vmmd.SetVersion(instance.ID, "cache-v1")

	bin := buildGregale(t)
	runGregaleForCacheE2E(t, bin, f.h.APIDURL, f.key,
		"cache", "GET", "/products/:id", "for", "10s",
		"--app", f.app.Slug,
		"--host", f.host,
		"--stale-while-revalidate", "30s",
		"--stale-if-error", "0s")
	// The rule-created notification or the two-second durable repair poll can
	// arrive after the first request and evict a just-filled response. Allow
	// two poll cycles before measuring the fresh and stale windows; otherwise
	// this test exercises a miss instead of stale-while-revalidate.
	time.Sleep(5 * time.Second)

	_, firstBody, firstStatus := waitForGatewayResponse(t, f, "/products/42", "normal-path:cache-v1\n", 10*time.Second)
	if firstStatus != 200 || string(firstBody) != "normal-path:cache-v1\n" {
		t.Fatalf("first cacheable response: status=%d body=%q", firstStatus, firstBody)
	}
	filledAt := f.vmmd.ForwardCount()
	if filledAt == 0 {
		t.Fatal("cache fill never reached the origin")
	}
	if keys := redis.Keys(); len(keys) == 0 {
		t.Fatal("gateway did not write the cache fill to Redis")
	}

	_, secondBody, secondStatus := doReqHeaders(t, f.h, f.host, http.MethodGet, "/products/42", nil)
	if secondStatus != 200 || string(secondBody) != "normal-path:cache-v1\n" {
		t.Fatalf("fresh cache hit: status=%d body=%q", secondStatus, secondBody)
	}
	if got := f.vmmd.ForwardCount(); got != filledAt {
		t.Fatalf("fresh hit reached origin: forward count=%d, want %d", got, filledAt)
	}

	time.Sleep(11 * time.Second)
	f.vmmd.SetVersion(instance.ID, "cache-v2")
	staleHeaders, staleBody, staleStatus := doReqHeaders(t, f.h, f.host, http.MethodGet, "/products/42", nil)
	if got := staleHeaders.Get("x-faas-cache"); got != "stale-while-revalidate" {
		t.Fatalf("SWR classification header = %q, status=%d body=%q", got, staleStatus, staleBody)
	}
	if staleStatus != 200 || string(staleBody) != "normal-path:cache-v1\n" {
		t.Fatalf("SWR response: status=%d body=%q", staleStatus, staleBody)
	}
	waitForForwardCount(t, f, filledAt+1, 10*time.Second)
	_, refreshedBody, _ := waitForGatewayResponse(t, f, "/products/42", "normal-path:cache-v2\n", 10*time.Second)
	if string(refreshedBody) != "normal-path:cache-v2\n" {
		t.Fatalf("background refresh body=%q, want cache-v2", refreshedBody)
	}

	runGregaleForCacheE2E(t, bin, f.h.APIDURL, f.key,
		"cache", "purge", f.app.Slug, "--path", "/products/*")
	waitForRedisEmpty(t, redis, 10*time.Second)
	beforePurgeMiss := f.vmmd.ForwardCount()
	f.vmmd.SetVersion(instance.ID, "cache-v3")
	_, purgedBody, _ := waitForGatewayResponse(t, f, "/products/42", "normal-path:cache-v3\n", 10*time.Second)
	if string(purgedBody) != "normal-path:cache-v3\n" {
		t.Fatalf("post-purge body=%q, want cache-v3", purgedBody)
	}
	if got := f.vmmd.ForwardCount(); got <= beforePurgeMiss {
		t.Fatalf("post-purge request did not reach origin: count=%d, before=%d", got, beforePurgeMiss)
	}
}

func runGregaleForCacheE2E(t *testing.T, bin, apiURL, token string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(),
		"FAAS_API="+apiURL,
		"FAAS_TOKEN="+token,
		"HOME="+t.TempDir(),
		"XDG_CONFIG_HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("gregale %s exited %d: %s", strings.Join(args, " "), exitErr.ExitCode(), out)
		}
		t.Fatalf("gregale %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func waitForGatewayResponse(t *testing.T, f *normalPathFixture, requestPath, want string, timeout time.Duration) (http.Header, []byte, int) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastHeader http.Header
	var lastBody []byte
	var lastStatus int
	for time.Now().Before(deadline) {
		header, body, status := doReqHeaders(t, f.h, f.host, http.MethodGet, requestPath, nil)
		lastHeader, lastBody, lastStatus = header, body, status
		if status == 200 && string(body) == want {
			return header, body, status
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("GET %s%s did not return %q within %s; last status=%d body=%q", f.host, requestPath, want, timeout, lastStatus, lastBody)
	return lastHeader, lastBody, lastStatus
}

func waitForForwardCount(t *testing.T, f *normalPathFixture, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f.vmmd.ForwardCount() >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("origin forward count=%d, want at least %d", f.vmmd.ForwardCount(), want)
}

func waitForRedisEmpty(t *testing.T, redis *miniredis.Miniredis, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(redis.Keys()) == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Redis cache still contains keys after purge: %v", redis.Keys())
}
