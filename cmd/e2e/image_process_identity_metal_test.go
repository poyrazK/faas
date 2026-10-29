//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestImageProcessIdentityMetal proves ADR-1239's Docker process identity
// rules in a real guest: an image without USER runs as root, and a numeric
// USER absent from the image's passwd runs with GID 0. The fixture image has
// no /etc/passwd at all.
func TestImageProcessIdentityMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping image process identity acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping image process identity acceptance")
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
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "full-rootfs")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	for _, tc := range []struct {
		name, slug, user string
		port             int
		wantUID, wantGID int
	}{
		{"no USER runs as root", "identity-root", "", 8080, 0, 0},
		// guest-init moves its metadata proxy off port 80 for an image that
		// serves it, as nginx and httpd do.
		{"no USER serves port 80", "identity-port80", "", 80, 0, 0},
		{"unknown numeric USER runs with group 0", "identity-uid", "1001", 8080, 1001, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			falsy := false
			if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: tc.slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
				t.Fatalf("create app: status=%d", got)
			}
			appID := mustGetAppID(t, h, key, tc.slug)
			setAppIdleTimeout(t, h, key, tc.slug, api.IdleTimeoutFloorSeconds)

			image, _ := e2etest.HelloImageWithProcessContractOnPort("library/"+tc.slug, "identity", tc.user, tc.port)
			ref := registry.AddImage("library/"+tc.slug, image)
			body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+tc.slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
			if status != http.StatusAccepted {
				t.Fatalf("create deployment: status=%d body=%s", status, body)
			}
			liveCtx, liveCancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer liveCancel()
			if _, err := e2etest.WaitForDeploymentLive(liveCtx, t, pool, parseImageDeployment(t, body), 75*time.Second); err != nil {
				t.Fatalf("deployment did not reach live: %v", err)
			}

			body, status = doGetWithHost(t, h.HTTPClient(), gatewayAppURL(h, tc.slug)+"contract", tc.slug+".apps.test.example", 30*time.Second)
			if status != http.StatusOK {
				t.Fatalf("process contract status=%d body=%s", status, body)
			}
			var got struct {
				UID    int    `json:"uid"`
				GID    int    `json:"gid"`
				Marker string `json:"marker"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if got.UID != tc.wantUID || got.GID != tc.wantGID || got.Marker != "container-contract" {
				t.Fatalf("process identity = %d:%d (marker %q), want %d:%d", got.UID, got.GID, got.Marker, tc.wantUID, tc.wantGID)
			}

			parkCtx, parkCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer parkCancel()
			parkApp(t, h, key, tc.slug)
			if _, err := e2etest.WaitForInstanceState(parkCtx, t, pool, appID, state.StateParked, 45*time.Second); err != nil {
				t.Fatalf("park: %v", err)
			}
		})
	}
}
