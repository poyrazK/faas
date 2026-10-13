//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestProfileCaptureMemoryPressureMetal grows the Go fixture's live heap in
// a real guest until guest-init reports memory pressure, then expects the
// platform to have captured the heap automatically (ADR-1155): guest-init →
// vmmd → schedd ReportMemoryPressure → queued capture → vmmd capture → apid.
func TestProfileCaptureMemoryPressureMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping memory pressure capture acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping memory pressure capture acceptance")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "profile-pressure")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_PROFILING_ON_DEMAND=1")
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	slug := "profmem-" + randHexSuffix()
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)
	image, _ := e2etest.HelloImage("library/"+slug, "pressure hello")
	ref := registry.AddImage("library/"+slug, image)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID := parseImageDeployment(t, body)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 110*time.Second); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	client, url, host := h.HTTPClient(), gatewayAppURL(h, slug), slug+".apps.test.example"
	if b, s := doGetWithHost(t, client, url, host, 60*time.Second); s != http.StatusOK || strings.TrimSpace(string(b)) != "pressure hello" {
		t.Fatalf("first request: status=%d body=%q", s, b)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 30*time.Second); err != nil {
		t.Fatalf("instance not running: %v", err)
	}

	// Grow slowly: the watcher samples every 2s, so small steps keep the
	// overshoot past the pressure threshold well short of an OOM kill.
	listPath := "/v1/apps/" + slug + "/profiles/captures"
	var auto *api.ProfileCapture
	grownMB := ""
	for step := 0; step < 120 && auto == nil; step++ {
		b, s := doGetWithHost(t, client, url+"grow?mb=4", host, 10*time.Second)
		if s != http.StatusOK {
			t.Fatalf("grow step %d: status=%d body=%q", step, s, b)
		}
		grownMB = strings.TrimSpace(string(b))
		time.Sleep(750 * time.Millisecond)
		lb, ls := doReq(t, h, key, http.MethodGet, listPath, nil)
		if ls != http.StatusOK {
			t.Fatalf("list captures: status=%d body=%s", ls, lb)
		}
		var list api.ProfileCaptureList
		if err := json.Unmarshal(lb, &list); err != nil {
			t.Fatal(err)
		}
		for i := range list.Captures {
			if list.Captures[i].Trigger == api.ProfileCaptureTriggerMemoryPressure {
				auto = &list.Captures[i]
			}
		}
	}
	if auto == nil {
		t.Fatalf("no automatic capture after growing the heap to %s MiB", grownMB)
	}
	t.Logf("automatic capture %s queued after %s MiB grown: %s", auto.ID, grownMB, auto.TriggerDetail)
	deadline := time.Now().Add(90 * time.Second)
	for !auto.Done() {
		if time.Now().After(deadline) {
			t.Fatalf("automatic capture stuck in %s", auto.Status)
		}
		time.Sleep(500 * time.Millisecond)
		b, s := doReq(t, h, key, http.MethodGet, listPath+"/"+auto.ID, nil)
		if s != http.StatusOK {
			t.Fatalf("get capture: status=%d body=%s", s, b)
		}
		if err := json.Unmarshal(b, auto); err != nil {
			t.Fatal(err)
		}
	}
	if auto.Status != api.ProfileCaptureReady || auto.Processes < 1 || auto.RequestedInstanceID == "" || len(auto.Kinds) != 1 || auto.Kinds[0] != "heap" {
		t.Fatalf("automatic capture = %+v; want a ready heap capture of the pressured instance", auto)
	}
	assertCaptureFunction(t, h, key, slug, auto.ID, "heap", "profileFixtureGrow")

	if _, s := doGetWithHost(t, client, url+"release", host, 10*time.Second); s != http.StatusNoContent {
		t.Fatalf("release: status=%d", s)
	}
	if _, s := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); s != http.StatusNoContent {
		t.Fatalf("cleanup park: status=%d", s)
	}
}
