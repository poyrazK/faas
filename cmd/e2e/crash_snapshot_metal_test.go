//go:build metal

// crash_snapshot_metal_test.go — ADR-733 crash snapshots end to end on real
// Firecracker: apid requests a capture of the running instance, schedd's
// CrashCaptureCoordinator captures it in place (the instance keeps serving
// and no snapshots row appears), imaged encrypts it at rest and deletes the
// plaintext, and the capture opens as an ADR-732 fork (imaged decrypts it
// for the fork) that a token-bearing request reaches through the gateway.
// Once the fork ends, the plaintext is purged again.
//
// Requires /dev/kvm, root, Firecracker on PATH and FAAS_TEST_KERNEL.
package e2e_test

// adr: 733

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCrashSnapshotMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal crash snapshot test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
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
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_APP_FORKS=1", "FAAS_CRASH_SNAPSHOTS=1",
		"FAAS_HOST_AGE_IDENTITY_PATH="+writeE2EAgeKeys(t))
	key := h.SeedAccount(context.Background(), api.PlanPro)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "hello")
	stopAppOnCleanup(t, h, pool, key, "hello", appID)
	// Pro defaults to bearer public auth (ADR-079); the anonymous probes
	// below exercise routing, not edge auth.
	if body, code := doReq(t, h, key, http.MethodPatch, "/v1/apps/hello", map[string]any{
		"require_authn": false, "public_auth": map[string]any{"mode": "open"},
	}); code != http.StatusOK {
		t.Fatalf("open public auth: %d %s", code, body)
	}
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	defer h.DumpLogs(t)
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 90*time.Second); err != nil {
		t.Fatalf("deployment not live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}
	client := h.HTTPClient()
	if err := e2etest.WaitForHTTPReady(ctx, t, client, gatewayAppURL(h, "hello"), 10*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	if body, status := doGetWithHost(t, client, gatewayAppURL(h, "hello"), "hello.apps.test.example", 60*time.Second); status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
		t.Fatalf("serving wake = %d %q", status, body)
	}
	serving, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 30*time.Second)
	if err != nil || len(serving) == 0 {
		t.Fatalf("no running serving instance: %v", err)
	}

	var capture api.CrashCaptureResponse
	t.Run("capture-in-place", func(t *testing.T) {
		raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/crash-snapshots", nil)
		if status != http.StatusAccepted {
			t.Fatalf("request capture = %d %s", status, raw)
		}
		if err := json.Unmarshal(raw, &capture); err != nil {
			t.Fatal(err)
		}
		capture = waitCrashCaptureStatus(ctx, t, h, key, capture.ID, "ready", 120*time.Second)
		ins, err := state.NewPgStore(pool).InstanceByID(ctx, serving[0].ID)
		if err != nil || ins.State != string(state.StateRunning) {
			t.Fatalf("serving instance after capture = %+v, %v; want still running", ins, err)
		}
		// imaged encrypts the capture and deletes the plaintext.
		sealed := waitCapturePlaintext(ctx, t, pool, capture.ID, state.CrashPlaintextAbsent, 60*time.Second)
		if len(sealed.SealedKey) == 0 || sealed.EncryptedAt == nil {
			t.Fatalf("capture not sealed: %+v", sealed)
		}
		mem := captureFile(*sealed.StorageKey)
		if _, err := os.Stat(mem); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("plaintext memory %s still on disk (err=%v)", mem, err)
		}
		if st, err := os.Stat(mem + ".age"); err != nil || st.Size() >= *sealed.MemBytes {
			t.Fatalf("encrypted memory: %v (size vs mem_bytes %d)", err, *sealed.MemBytes)
		}
		// Only the deploy's own captures may exist as snapshots rows; the crash
		// capture key must not be among them.
		for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
			snap, err := state.NewPgStore(pool).LatestSnapshotForTier(ctx, dep.ID, tier)
			if err == nil && strings.Contains(snap.StorageKey, capture.ID) {
				t.Fatalf("crash capture became a %s snapshots row: %s", tier, snap.StorageKey)
			} else if err != nil && !errors.Is(err, state.ErrNotFound) {
				t.Fatal(err)
			}
		}
	})

	t.Run("open-as-fork-and-reach-it", func(t *testing.T) {
		raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/crash-snapshots/"+capture.ID+"/fork", api.CreateAppForkRequest{})
		if status != http.StatusAccepted {
			t.Fatalf("fork capture = %d %s", status, raw)
		}
		var fork api.AppForkResponse
		if err := json.Unmarshal(raw, &fork); err != nil || fork.AccessToken == "" {
			t.Fatalf("decode fork: %v %s", err, raw)
		}
		waitForkStatus(ctx, t, h, key, fork.ID, "running", 120*time.Second)
		stored, err := state.NewPgStore(pool).AppForkForApp(ctx, appID, fork.ID)
		if err != nil || stored.CrashCaptureID == nil || *stored.CrashCaptureID != capture.ID {
			t.Fatalf("fork row = %+v, %v; want pinned to the capture", stored, err)
		}
		headers, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/", nil,
			map[string]string{api.ForkHeader: fork.ID, api.ForkTokenHeader: fork.AccessToken})
		if status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody || headers.Get("X-Gregale-Fork-Served") != "1" {
			t.Fatalf("request to the crash fork = %d %q", status, body)
		}
		if c := waitCapturePlaintext(ctx, t, pool, capture.ID, state.CrashPlaintextStaged, 10*time.Second); c.SealedKey == nil {
			t.Fatalf("staged capture lost its sealed key: %+v", c)
		}

		// Once the fork ends, the plaintext goes again.
		if _, status := doReq(t, h, key, http.MethodDelete, "/v1/apps/hello/forks/"+fork.ID, nil); status != http.StatusAccepted {
			t.Fatalf("cancel fork = %d", status)
		}
		waitForkStatus(ctx, t, h, key, fork.ID, "cancelled", 60*time.Second)
		purged := waitCapturePlaintext(ctx, t, pool, capture.ID, state.CrashPlaintextAbsent, 60*time.Second)
		if _, err := os.Stat(captureFile(*purged.StorageKey)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("plaintext memory outlived the fork (err=%v)", err)
		}
	})
}

// writeE2EAgeKeys writes the fleet and host identities imaged loads to seal
// crash capture keys, and returns the host identity path.
func writeE2EAgeKeys(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"fleet.age", "host.age"} {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		if err := secretbox.WriteHostKeyAtPath(filepath.Join(dir, name), id); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "host.age")
}

// captureFile is where the local storage backend keeps a capture object.
func captureFile(key string) string {
	root := os.Getenv("FAAS_STORAGE_ROOT")
	if root == "" {
		root = "/srv/fc"
	}
	return filepath.Join(root, key)
}

func waitCapturePlaintext(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id string, want state.CrashCapturePlaintext, timeout time.Duration) state.CrashCapture {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last state.CrashCapture
	for time.Now().Before(deadline) && ctx.Err() == nil {
		c, err := state.NewPgStore(pool).CrashCaptureForRestore(ctx, id)
		if err == nil {
			last = c
			if c.PlaintextState == want {
				return c
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("capture %s plaintext = %q after %s, want %q", id, last.PlaintextState, timeout, want)
	return last
}

func waitCrashCaptureStatus(ctx context.Context, t *testing.T, h *e2etest.Harness, key, id, want string, timeout time.Duration) api.CrashCaptureResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last api.CrashCaptureResponse
	for time.Now().Before(deadline) && ctx.Err() == nil {
		raw, status := doReq(t, h, key, http.MethodGet, "/v1/apps/hello/crash-snapshots/"+id, nil)
		if status == http.StatusOK && json.Unmarshal(raw, &last) == nil {
			if last.Status == want {
				return last
			}
			if last.Failure != nil {
				t.Fatalf("capture %s failed: %s %s", id, last.Failure.Code, last.Failure.Message)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("capture %s status = %q after %s, want %q", id, last.Status, timeout, want)
	return last
}
