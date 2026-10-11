// adr: 950
//
// End-to-end coverage for opt-in depends_on wake-ahead. A service call wakes a
// parked `auth`; when `auth` opted in, the gateway must also restore its
// declared dependency `billing` even though nothing ever calls `billing`.
//
// Build tag: (none). Requires Postgres (skip via FAAS_SKIP_PG_TESTS). No KVM:
// the fake vmmd answers for the restored guests.
package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// parkServiceApp gives an app a live deployment with a published layer and no
// running instance: the state scale-to-zero leaves it in.
func parkServiceApp(t *testing.T, f *normalPathFixture, appID, digestByte string) {
	t.Helper()
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID:       appID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat(digestByte, 64),
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, dep.ID); err != nil {
		t.Fatalf("mark deployment live: %v", err)
	}
	publishNormalPathLayer(t, f, dep.ID)
}

func setServiceManifest(t *testing.T, f *normalPathFixture, appID string, wakeAhead api.ServiceWakeAhead, deps ...string) {
	t.Helper()
	app, err := f.store.AppByID(f.ctx, appID)
	if err != nil {
		t.Fatalf("load app: %v", err)
	}
	manifest := app.Manifest
	manifest.ServiceWakeAhead = wakeAhead
	manifest.ServiceBindings = nil
	for _, dep := range deps {
		manifest.ServiceBindings = append(manifest.ServiceBindings, api.AppServiceBinding{Binding: dep, Service: dep})
	}
	if _, err := f.store.UpdateApp(f.ctx, appID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatalf("update manifest: %v", err)
	}
}

func wakeTriggerHistogram(t *testing.T, f *normalPathFixture, slug string) map[string]int {
	t.Helper()
	body, status := doReq(t, f.h, f.key, http.MethodGet, "/v1/apps/"+slug+"/wake-timeline", nil)
	if status != http.StatusOK {
		t.Fatalf("wake timeline %s: status=%d body=%s", slug, status, body)
	}
	var timeline api.AppWakeTimelineResponse
	if err := json.Unmarshal(body, &timeline); err != nil {
		t.Fatalf("decode wake timeline: %v body=%s", err, body)
	}
	return timeline.TriggerHistogram
}

func TestE2E_ServiceWakeAhead_RestoresDeclaredDependency(t *testing.T) {
	cases := []struct {
		name      string
		wakeAhead api.ServiceWakeAhead
		wantWoken bool
	}{
		{name: "declared", wakeAhead: api.ServiceWakeAheadDeclared, wantWoken: true},
		{name: "off", wakeAhead: "", wantWoken: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newNormalPathFixtureWithPlanAndEnv(t, "wa-caller-"+tc.name, api.PlanPro, "FAAS_GATEWAY_SERVICE_WAKE_AHEAD=true")
			if f == nil {
				return
			}
			f.vmmd.SetDefaultVersion("v1")
			authSlug, billingSlug := "wa-auth-"+tc.name, "wa-billing-"+tc.name
			auth := createServiceApp(t, f, authSlug, nil)
			billing := createServiceApp(t, f, billingSlug, nil)
			parkServiceApp(t, f, auth.ID, "4")
			parkServiceApp(t, f, billing.ID, "5")
			setServiceManifest(t, f, auth.ID, tc.wakeAhead, billingSlug)

			status, _, body := pollServiceCall(t, f.h, f.app.ID, authSlug, "/", http.StatusOK, 30*time.Second)
			if status != http.StatusOK {
				t.Fatalf("call auth: status=%d body=%s", status, body)
			}

			if !tc.wantWoken {
				// Give a wrongly-enabled wake-ahead time to land before asserting
				// it did not happen.
				time.Sleep(3 * time.Second)
				if running, err := f.store.RunningInstanceForApp(f.ctx, billing.ID); err == nil {
					t.Fatalf("billing woke without opt-in (instance %s)", running.ID)
				}
				return
			}
			deadline := time.Now().Add(30 * time.Second)
			for {
				if _, err := f.store.RunningInstanceForApp(f.ctx, billing.ID); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("billing was never restored by auth's wake-ahead")
				}
				time.Sleep(200 * time.Millisecond)
			}
			for {
				hist := wakeTriggerHistogram(t, f, billingSlug)
				if hist["service.wake_ahead"] > 0 {
					if hist["service.mesh"] != 0 {
						t.Fatalf("billing trigger histogram = %v; nothing called billing", hist)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("billing wake timeline never recorded service.wake_ahead: %v", hist)
				}
				time.Sleep(200 * time.Millisecond)
			}
		})
	}
}
