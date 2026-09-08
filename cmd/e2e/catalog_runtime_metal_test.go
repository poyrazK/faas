//go:build metal

package e2e_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingcontract"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestCatalogRuntimeParityMetal turns the maintained source catalog into a
// reference-node acceptance matrix. The default quick subset covers one OCI,
// one Node, and one Go source shape; the full matrix is available to the
// nightly/reference-node job with FAAS_E2E_API_HOSTING_CATALOG=full.
//
// Each selected fixture is exercised through the customer path:
// source archive -> build -> Live -> receipt/readiness smoke -> public HTTP
// request -> park -> snapshot wake -> public HTTP request -> park for cleanup.
func TestCatalogRuntimeParityMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping catalog runtime parity")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping catalog runtime parity")
	}

	catalog, err := apihostingcontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	full := strings.EqualFold(strings.TrimSpace(os.Getenv("FAAS_E2E_API_HOSTING_CATALOG")), "full")
	selected := make([]apihostingcontract.Fixture, 0)
	for _, fixture := range catalog.Fixtures {
		if !hasCatalogTag(fixture, "runtime") || (!full && !hasCatalogTag(fixture, "quick")) {
			continue
		}
		selected = append(selected, fixture)
	}
	if len(selected) == 0 {
		t.Fatal("catalog runtime parity selected no fixtures")
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	h := e2etest.Start(t, pool, e2etest.All)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	for _, fixture := range selected {
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			t.Cleanup(func() {
				if t.Failed() {
					h.DumpLogs(t)
				}
			})
			slug := "catalog-" + fixture.ID
			result := runBuildSubtest(t, h, pool, key, slug, "", catalogFixtureTarball(t, fixture), hasCatalogFile(fixture, "Dockerfile"))
			assertCatalogReceipt(t, pool, result, fixture)

			appID := mustGetAppID(t, h, key, slug)
			setAppIdleTimeout(t, h, key, slug, api.IdleTimeoutFloorSeconds)
			assertCatalogPublicRequest(t, h, result, slug)

			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusAccepted {
				t.Fatalf("park %s: status=%d", slug, status)
			}
			if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 40*time.Second); err != nil {
				t.Fatalf("park %s: %v", slug, err)
			}

			wakeCtx, wakeCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer wakeCancel()
			body, wakeID, status := doGetWithHostCapturingWakeID(t, h.HTTPClient(), gatewayAppURL(h, slug), slug+".apps.test.example", 60*time.Second)
			if status < http.StatusOK || status >= http.StatusMultipleChoices {
				t.Fatalf("wake request %s: status=%d body=%s", slug, status, body)
			}
			if wakeID == "" {
				t.Fatalf("wake request %s missing x-faas-wake-id", slug)
			}
			if _, err := e2etest.WaitForWakeMethod(wakeCtx, t, pool, wakeID, "restore", 45*time.Second); err != nil {
				t.Fatalf("wake %s did not restore: %v", slug, err)
			}
			assertCatalogPublicRequest(t, h, result, slug)

			// Leave no resident instance behind for the next catalog case.
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cleanupCancel()
			if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusAccepted {
				t.Fatalf("cleanup park %s: status=%d", slug, status)
			}
			if _, err := e2etest.WaitForInstanceState(cleanupCtx, t, pool, appID, state.StateParked, 40*time.Second); err != nil {
				t.Fatalf("cleanup park %s: %v", slug, err)
			}
		})
	}
}

func hasCatalogTag(fixture apihostingcontract.Fixture, want string) bool {
	for _, tag := range fixture.Tags {
		if tag == want {
			return true
		}
	}
	return false
}

func hasCatalogFile(fixture apihostingcontract.Fixture, want string) bool {
	_, ok := fixture.Files[want]
	return ok
}

func catalogFixtureTarball(t *testing.T, fixture apihostingcontract.Fixture) []byte {
	t.Helper()
	return buildTarGz(t, fixture.Files)
}

func assertCatalogReceipt(t *testing.T, pool *pgxpool.Pool, result buildResult, fixture apihostingcontract.Fixture) {
	t.Helper()
	store := state.NewPgStore(pool)
	dep, err := store.DeploymentByID(context.Background(), result.deploymentID)
	if err != nil {
		t.Fatalf("deployment %s: %v", result.deploymentID, err)
	}
	receipt, err := apihostingreceipt.Decode(dep.APIHostingReceipt)
	if err != nil {
		t.Fatalf("deployment %s hosting receipt: %v", result.deploymentID, err)
	}
	want := fixture.Expected
	got := receipt.Profile
	if got.Framework != want.Framework || got.PackageManager != want.PackageManager || got.StartCommand != want.StartCommand || got.Port != want.Port || got.HealthPath != want.HealthPath || got.ConfigFile != want.ConfigFile || got.Inferred != want.Inferred {
		t.Fatalf("fixture %s profile=%+v, want framework=%q package_manager=%q command=%q port=%d health=%q config=%q inferred=%t", fixture.ID, got, want.Framework, want.PackageManager, want.StartCommand, want.Port, want.HealthPath, want.ConfigFile, want.Inferred)
	}
	if receipt.Smoke.Status != apihostingreceipt.SmokeVerified || receipt.Smoke.Path != want.HealthPath || receipt.Smoke.StatusCode < http.StatusOK || receipt.Smoke.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("fixture %s smoke=%+v, want verified %s 2xx", fixture.ID, receipt.Smoke, want.HealthPath)
	}
}

func assertCatalogPublicRequest(t *testing.T, h *e2etest.Harness, result buildResult, slug string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := gatewayAppURL(h, slug)
	if err := e2etest.WaitForHTTPReady(ctx, t, h.HTTPClient(), url, 5*time.Second); err != nil {
		t.Fatalf("gateway route for %s: %v", slug, err)
	}
	body, status := doGetWithHost(t, h.HTTPClient(), url, slug+".apps.test.example", 30*time.Second)
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		t.Fatalf("public request for %s: status=%d body=%s", slug, status, body)
	}
	if strings.TrimSpace(string(body)) == "" {
		t.Fatalf("public request for %s returned an empty body", slug)
	}
	// build-done.json is the durable build-log evidence retained by the
	// reference harness. Requiring a successful record keeps the matrix from
	// passing on a deployment that reached Live through an incomplete build.
	done := readBuildDone(t, h, result.buildID)
	if done.ExitCode != 0 {
		t.Fatalf("build %s exit_code=%d", result.buildID, done.ExitCode)
	}
}
