//go:build metal

// adr: 196 — a service call that lands inside the node-local proxy's endpoint
// lease after its target was parked must wake the target, not answer
// "503 service has no healthy replicas". Real guests, real park: the proxy
// holds the parked instance in its 5 s lease, the bridge reports it gone, and
// the proxy must evict it and re-route through the wake path.
package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// staleLeaseBudget is how long after the lease-refreshing call the post-park
// call may land and still exercise the stale lease. The proxy lease is 5 s
// (serviceProxyDefaultEndpointTTL); the margin keeps clock jitter out.
const staleLeaseBudget = 4500 * time.Millisecond

// staleLeaseFixture is one caller app ("edge") and one live target app
// ("target") on real guests.
type staleLeaseFixture struct {
	h      *e2etest.Harness
	edgeID string
	park   func()
}

func newStaleLeaseFixture(t *testing.T) *staleLeaseFixture {
	t.Helper()
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return nil
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	builderBaseRef := registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideBuilderBase(t, builderBaseRef)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	key := h.SeedAccount(context.Background(), api.PlanPro)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	for _, slug := range []string{"edge", "target"} {
		if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
			t.Fatalf("create %s: status=%d", slug, got)
		}
	}
	edgeID := mustGetAppID(t, h, key, "edge")
	targetID := mustGetAppID(t, h, key, "target")
	rawDep, status := doReq(t, h, key, http.MethodPost, "/v1/apps/target/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("deploy target: status=%d body=%s", status, rawDep)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(rawDep, &dep); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	// The first deployment on a shared node can re-stage base images, so the
	// deadline is generous; only the calls below are under test.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 10*time.Minute); err != nil {
		h.DumpLogs(t)
		t.Fatalf("target deployment did not reach live: %v", err)
	}
	park := func() {
		t.Helper()
		if raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/target/park", nil); status/100 != 2 {
			t.Fatalf("park: status=%d body=%s", status, raw)
		}
		pctx, pcancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer pcancel()
		if _, err := e2etest.WaitForAppParked(pctx, t, pool, targetID, 40*time.Second); err != nil {
			t.Fatalf("target not parked: %v", err)
		}
	}
	t.Cleanup(park)

	if status, _, body := pollServiceCall(t, h, edgeID, "target", "/", http.StatusOK, 2*time.Minute); status != http.StatusOK {
		t.Fatalf("warm-up call: status=%d body=%s", status, body)
	}
	return &staleLeaseFixture{h: h, edgeID: edgeID, park: park}
}

// runStaleLeaseCycles parks the target right after a call refreshed the
// proxy's lease on it, then makes call while that lease still names the
// parked instance. check reports why the call's result is wrong, or "".
func (f *staleLeaseFixture) runStaleLeaseCycles(t *testing.T, call func() (int, string), check func(status int, body string) string) {
	t.Helper()
	const cycles = 3
	informative := 0
	for i := 0; i < cycles; i++ {
		// Let any previous lease expire, then refresh it with a call that
		// reaches the running instance: the proxy now holds that instance.
		// Cycles are deliberately closer together than the 10 s ADR-201
		// retry-budget bucket: the re-route after vmmd's NotFound is
		// undelivered (ADR-1133) and must not spend a quiet app's allowance.
		time.Sleep(5500 * time.Millisecond)
		if status, _, body := pollServiceCall(t, f.h, f.edgeID, "target", "/", http.StatusOK, time.Minute); status != http.StatusOK {
			t.Fatalf("cycle %d: lease-refreshing call: status=%d body=%s", i, status, body)
		}
		leased := time.Now()
		f.park()
		time.Sleep(time.Second)
		elapsed := time.Since(leased)
		status, body := call()
		if elapsed > staleLeaseBudget {
			t.Logf("cycle %d: park took too long (%s after the lease refresh); lease expired, cycle not informative (status=%d)", i, elapsed.Round(time.Millisecond), status)
			continue
		}
		informative++
		t.Logf("cycle %d: call %s after the lease refresh, target parked: status=%d", i, elapsed.Round(time.Millisecond), status)
		if problem := check(status, body); problem != "" {
			t.Errorf("cycle %d: call inside the stale lease: %s", i, problem)
		}
	}
	if informative == 0 {
		t.Fatalf("no cycle landed inside the %s lease budget; the stale-lease path was not exercised", staleLeaseBudget)
	}
}

func TestServiceCallInsideLeaseAfterParkWakesMetal(t *testing.T) {
	f := newStaleLeaseFixture(t)
	if f == nil {
		return
	}
	f.runStaleLeaseCycles(t,
		func() (int, string) {
			status, _, body := serviceCall(t, f.h, f.edgeID, "target", "/")
			return status, body
		},
		func(status int, body string) string {
			if status != http.StatusOK || strings.TrimSpace(body) != helloBody {
				return fmt.Sprintf("got %d %q, want 200 %q (the parked target must be woken)", status, strings.TrimSpace(body), helloBody)
			}
			return ""
		})
}

// ADR-1133: a POST with a small declared-length body that lands on the stale
// lease is buffered by the proxy, so the re-route after vmmd's NotFound can
// replay it to the woken target with the body intact.
func TestServicePostInsideLeaseAfterParkReplaysBodyMetal(t *testing.T) {
	f := newStaleLeaseFixture(t)
	if f == nil {
		return
	}
	payload := make([]byte, 256<<10) // well under ServiceProxyReplayBodyMaxBytes
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	f.runStaleLeaseCycles(t,
		func() (int, string) {
			return servicePost(t, f.h, f.edgeID, "target", "/echo", payload)
		},
		func(status int, body string) string {
			if status != http.StatusOK {
				return fmt.Sprintf("got %d %q, want 200 (the parked target must be woken and the POST replayed)", status, truncateForLog(body))
			}
			if !bytes.Equal([]byte(body), payload) {
				return fmt.Sprintf("echoed %d bytes, want the %d-byte request body back unchanged", len(body), len(payload))
			}
			return ""
		})
}

// servicePost sends a declared-length POST through the internal service proxy.
func servicePost(t *testing.T, h *e2etest.Harness, callerAppID, service, path string, payload []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.GatewayControlURL+"/v1/internal/services/"+service+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build service request: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Faas-Caller-App", callerAppID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("service call: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func truncateForLog(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
