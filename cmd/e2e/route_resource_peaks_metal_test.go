//go:build metal

// route_resource_peaks_metal_test.go — ADR-20261121170000 native acceptance:
// an app whose /grow route retains 256 MB once, beside steady light traffic,
// must come back from GET /v1/apps/{slug}/analytics/route-resources with
// /grow owning the memory peak:
//
//	guest-init cgroup sampler → guest event 0x0C → vmmd resource minutes →
//	apid IngestInstanceResourceMinutes ┐
//	gateway instance route minutes → apid AddInstanceRouteMinutes ┴→ join
//
// Build tag: metal. Requires /dev/kvm + root, FAAS_TEST_KERNEL and
// FAAS_BUILDER_BASE_PATH.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261121170000 — the route that raised the app's memory owns its peak.
func TestRouteResourcePeaksMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping route resource peaks acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping route resource peaks acceptance")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sockets, err := os.MkdirTemp("", "faas-e2e-rrpeaks-*")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	env := []string{
		"FAAS_OTEL_SPANS_WRITER_ENABLED=true",
		"FAAS_APID_OTEL_SPANS_WRITER_SOCKET=" + filepath.Join(sockets, "spans.sock"),
		"FAAS_REQUEST_TELEMETRY_ENABLED=true",
		"FAAS_APID_REQUEST_TELEMETRY_SOCKET=" + filepath.Join(sockets, "request-telemetry.sock"),
	}

	const slug = "route-peaks"
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", slug)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.StartWithAPIDEnv(t, pool, e2etest.DeployWake, nil, env...)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	releasePlatformFlags(t, pool, []string{platformflags.RouteResourcePeaks}, "route resource peaks acceptance")
	key := h.SeedAccount(context.Background(), api.PlanPro)

	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)
	image, _ := e2etest.HelloImage("library/"+slug, "light")
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments",
		api.CreateDeploymentRequest{Image: registry.AddImage("library/"+slug, image)})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID := parseImageDeployment(t, body)
	deployCtx, deployCancel := context.WithTimeout(context.Background(), 320*time.Second)
	defer deployCancel()
	if _, err := e2etest.WaitForDeploymentLive(deployCtx, t, pool, depID, 300*time.Second); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cleanupCancel()
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Errorf("cleanup park: status=%d", status)
			return
		}
		if _, err := e2etest.WaitForInstanceState(cleanupCtx, t, pool, appID, state.StateParked, 45*time.Second); err != nil {
			t.Errorf("cleanup park: %v", err)
		}
	})

	host := slug + ".apps.test.example"
	get := func(path string) (int, string) {
		_, b, s := doReqHeaders(t, h, host, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + key})
		return s, string(b)
	}
	// Wake the app and keep it awake with light traffic: two quiet minutes,
	// one minute in which /grow retains 256 MB (a rise inside the minute),
	// then /release and two more quiet minutes.
	wakeDeadline := time.Now().Add(120 * time.Second)
	for {
		if s, _ := get("/"); s == http.StatusOK {
			break
		}
		if time.Now().After(wakeDeadline) {
			t.Fatal("app never answered /")
		}
		time.Sleep(2 * time.Second)
	}
	light := func(until time.Time) {
		for time.Now().Before(until) {
			if s, b := get("/"); s != http.StatusOK {
				t.Logf("light request: status=%d body=%q", s, b)
			}
			time.Sleep(3 * time.Second)
		}
	}
	nextMinute := func() time.Time { return time.Now().UTC().Truncate(time.Minute).Add(time.Minute) }
	light(nextMinute().Add(time.Minute)) // finish this minute plus one quiet minute
	heavyMinute := time.Now().UTC().Truncate(time.Minute)
	light(time.Now().Add(10 * time.Second))
	for i := 0; i < 4; i++ {
		if s, b := get("/grow?mb=64"); s != http.StatusOK {
			t.Fatalf("grow: status=%d body=%q", s, b)
		}
		time.Sleep(time.Second)
	}
	// /release goes in the next minute, so it shares no peak with /grow
	// (the first acceptance run sent it inside the heavy minute).
	light(heavyMinute.Add(70 * time.Second))
	if s, b := get("/release"); s != http.StatusNoContent && s != http.StatusOK {
		t.Fatalf("release: status=%d body=%q", s, b)
	}
	light(heavyMinute.Add(3 * time.Minute))
	t.Logf("heavy minute %s", heavyMinute.Format(time.RFC3339))

	// Closed minutes reach apid after RouteResourceMinuteGrace plus one flush
	// on both sides; poll until the heavy minute is joined.
	var report api.RouteResourcesResponse
	deadline := time.Now().Add(3 * time.Minute)
	for {
		b, s := doReq(t, h, key, http.MethodGet, "/v1/apps/"+slug+"/analytics/route-resources?since=1h", nil)
		if s != http.StatusOK {
			t.Fatalf("route resources: status=%d body=%s", s, b)
		}
		if err := json.Unmarshal(b, &report); err != nil {
			t.Fatalf("decode: %v body=%s", err, b)
		}
		if grow := findRoute(report.Routes, "/grow"); grow != nil && grow.HighMemoryMinutes > 0 && report.ResourceMinutes >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("route resources never joined the /grow peak: %s", b)
		}
		time.Sleep(10 * time.Second)
	}
	raw, _ := json.Marshal(report)
	t.Logf("route resources: %s", raw)

	grow := findRoute(report.Routes, "/grow")
	if grow.HighMemoryShare < 0.99 || grow.MemRiseP95WithMB < 128 {
		t.Fatalf("/grow does not own the peak: %+v", *grow)
	}
	if grow.PresenceShare >= 0.5 {
		t.Fatalf("/grow ran in %.0f%% of minutes; the fixture sends it in one", grow.PresenceShare*100)
	}
	if report.MemPeakMaxMB < 256 || report.MemLimitMB < 512 || report.CPULimitMillicores == 0 {
		t.Fatalf("app peak %d MB of limit %d MB, CPU limit %d; want >= 256 MB of a known limit", report.MemPeakMaxMB, report.MemLimitMB, report.CPULimitMillicores)
	}
	if release := findRoute(report.Routes, "/release"); release != nil && release.HighMemoryMinutes > 0 {
		t.Fatalf("/release shares a peak it ran after: %+v", *release)
	}
	// Quiet minutes after the heavy one start high (Go keeps the heap) but do
	// not rise, so the light route owns no peak they did not share.
	if root := findRoute(report.Routes, "/"); root != nil && root.HighMemoryMinutes > grow.HighMemoryMinutes {
		t.Fatalf("light route has more peaks than /grow: %+v", *root)
	}
	var rows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM instance_route_minutes WHERE app_id = $1`, appID).Scan(&rows); err != nil || rows == 0 {
		t.Fatalf("instance_route_minutes rows=%d err=%v", rows, err)
	}
}

// findRoute returns the report row whose route is path (with or without a
// method prefix), or nil.
func findRoute(routes []routeresources.RouteReport, path string) *routeresources.RouteReport {
	for i := range routes {
		r := routes[i].Route
		if r == path || strings.HasSuffix(r, " "+path) {
			return &routes[i]
		}
	}
	return nil
}
