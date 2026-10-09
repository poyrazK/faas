//go:build metal

// crash_snapshot_sdk_metal_test.go — ADR-733 SDK trigger end to end on real
// Firecracker: the app's /boom handler calls the guest metadata endpoint
// (POST 169.254.169.254/v1/crash-snapshots:capture) from inside the
// request, guest-init forwards it over vsock, vmmd writes an `sdk` request
// for that instance, schedd captures it while the call blocks, and the call
// returns `captured`. The capture opens as a fork in which the same /boom
// call resumes and reports `in_fork`.
//
// Requires /dev/kvm, root, Firecracker on PATH and FAAS_TEST_KERNEL.
package e2e_test

// adr: 733

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

type sdkCaptureAnswer struct {
	Status    string `json:"status"`
	CaptureID string `json:"capture_id"`
	InFork    bool   `json:"in_fork"`
}

func TestCrashSnapshotSDKMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal crash snapshot SDK test")
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
	img, _ := e2etest.CrashCaptureImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "hello")
	stopAppOnCleanup(t, h, pool, key, "hello", appID)
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

	var answer sdkCaptureAnswer
	t.Run("refused-without-opt-in", func(t *testing.T) {
		_, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/boom", nil)
		if status != http.StatusInternalServerError || json.Unmarshal(body, &answer) != nil || answer.Status != "refused" {
			t.Fatalf("/boom without opt-in = %d %s, want 500 with refused", status, body)
		}
	})

	t.Run("app-captures-itself-mid-request", func(t *testing.T) {
		if body, code := doReq(t, h, key, http.MethodPut, "/v1/apps/hello/crash-snapshots/settings", map[string]any{"enabled": true}); code != http.StatusOK {
			t.Fatalf("enable crash snapshots: %d %s", code, body)
		}
		_, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/boom", nil)
		if status != http.StatusInternalServerError || json.Unmarshal(body, &answer) != nil || answer.Status != "captured" || answer.InFork || answer.CaptureID == "" {
			t.Fatalf("/boom = %d %s, want 500 with captured outside a fork", status, body)
		}
		capture := waitCrashCaptureStatus(ctx, t, h, key, answer.CaptureID, "ready", 30*time.Second)
		if capture.Trigger != "sdk" || capture.Reason != "fixture boom" || capture.Route != "/boom" || capture.StatusCode != nil {
			t.Fatalf("capture = %+v, want an sdk capture with the app's reason and route", capture)
		}
		// The 5xx that /boom answered with must not add a second capture.
		raw, code := doReq(t, h, key, http.MethodGet, "/v1/apps/hello/crash-snapshots", nil)
		var list api.CrashCaptureListResponse
		if code != http.StatusOK || json.Unmarshal(raw, &list) != nil || len(list.Items) != 1 {
			t.Fatalf("captures = %d %s, want exactly the sdk one", code, raw)
		}
	})

	t.Run("fork-resumes-inside-the-call", func(t *testing.T) {
		raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/crash-snapshots/"+answer.CaptureID+"/fork", api.CreateAppForkRequest{})
		if status != http.StatusAccepted {
			t.Fatalf("fork capture = %d %s", status, raw)
		}
		var fork api.AppForkResponse
		if err := json.Unmarshal(raw, &fork); err != nil || fork.AccessToken == "" {
			t.Fatalf("decode fork: %v %s", err, raw)
		}
		waitForkStatus(ctx, t, h, key, fork.ID, "running", 120*time.Second)
		forkHeaders := map[string]string{api.ForkHeader: fork.ID, api.ForkTokenHeader: fork.AccessToken}
		deadline := time.Now().Add(20 * time.Second)
		for {
			_, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/last-capture", nil, forkHeaders)
			var got sdkCaptureAnswer
			if status == http.StatusOK && json.Unmarshal(body, &got) == nil && got.InFork {
				if got.Status != "captured" || got.CaptureID != answer.CaptureID {
					t.Fatalf("fork's /boom answer = %s, want captured %s in_fork", body, answer.CaptureID)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("fork never resumed inside /boom: last /last-capture = %d %s", status, body)
			}
			time.Sleep(500 * time.Millisecond)
		}
		// The serving instance's own record is unchanged.
		_, body, _ := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/last-capture", nil)
		var serving sdkCaptureAnswer
		if json.Unmarshal(body, &serving) != nil || serving.InFork || serving.CaptureID != answer.CaptureID {
			t.Fatalf("serving /last-capture = %s, want the captured answer outside a fork", body)
		}
		if _, status := doReq(t, h, key, http.MethodDelete, "/v1/apps/hello/forks/"+fork.ID, nil); status != http.StatusAccepted {
			t.Fatalf("cancel fork = %d", status)
		}
		waitForkStatus(ctx, t, h, key, fork.ID, "cancelled", 60*time.Second)
	})
}
