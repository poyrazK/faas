package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProfileCaptureOOMLink(t *testing.T) {
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store profileCaptureBackend = state.NewMemStore()
			if backend == "pg" {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				store = state.NewPgStore(pool)
			}
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "profile-oom@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "profile-oom", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			instance, deployment := uuid.NewString(), uuid.NewString()
			// started runs one capture on the instance up to capturing, then
			// optionally finishes it ready.
			started := func(at time.Time, kinds []string, finish bool) string {
				t.Helper()
				c := api.ProfileCapture{ID: uuid.NewString(), Kinds: kinds, DurationSeconds: 10, CreatedAt: at, ExpiresAt: at.Add(api.ProfileCaptureRetention)}
				if err := store.CreateProfileCapture(ctx, acct.ID, app.ID, c, profileCaptureStorage); err != nil {
					t.Fatal(err)
				}
				if _, err := store.ClaimProfileCapture(ctx, at); err != nil {
					t.Fatal(err)
				}
				if err := store.StartProfileCapture(ctx, c.ID, instance, deployment); err != nil {
					t.Fatal(err)
				}
				if finish {
					result := api.ProfileCapture{Status: api.ProfileCaptureReady, InstanceID: instance, DeploymentID: deployment, Processes: 1}
					if err := store.FinishProfileCapture(ctx, c.ID, result, nil, at.Add(12*time.Second)); err != nil {
						t.Fatal(err)
					}
				}
				return c.ID
			}
			if err := store.StartProfileCapture(ctx, uuid.NewString(), instance, deployment); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unknown capture started: %v", err)
			}
			started(now.Add(-10*time.Minute), []string{api.ProfileKindHeap}, true)
			started(now.Add(-5*time.Minute), []string{api.ProfileKindCPU}, true) // newer but CPU only
			running := started(now.Add(-time.Minute), []string{api.ProfileKindHeap}, false)

			got, err := store.GetProfileCapture(ctx, acct.ID, app.ID, running)
			if err != nil || got.InstanceID != instance || got.DeploymentID != deployment || got.Status != api.ProfileCaptureCapturing {
				t.Fatalf("started capture: %+v %v", got, err)
			}
			since := now.Add(-api.ProfileCaptureOOMLinkWindow)
			oom := state.ProfileCaptureOOM{KilledAt: now, PeakMB: 263, PlanMB: 256}
			for range 2 { // a redelivered OOM report re-stamps the same capture
				id, err := store.StampProfileCaptureOOM(ctx, app.ID, instance, oom, since)
				if err != nil || id != running {
					t.Fatalf("stamp = %q, %v; want %q", id, err, running)
				}
			}
			if _, err := store.StampProfileCaptureOOM(ctx, app.ID, uuid.NewString(), oom, since); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("other instance stamped: %v", err)
			}
			if _, err := store.StampProfileCaptureOOM(ctx, app.ID, instance, oom, now); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("capture outside the window stamped: %v", err)
			}
			if _, err := store.LatestOOMProfileCapture(ctx, acct.ID, app.ID, deployment); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("capturing row offered as the OOM profile: %v", err)
			}

			// The interrupted capture finishes with checkpoints; the OOM link
			// must survive the result merge.
			result := api.ProfileCapture{Status: api.ProfileCaptureReady, InstanceID: instance, DeploymentID: deployment, Processes: 1, Partial: true,
				Reason: "the workload was OOM-killed during the capture", Profiles: []api.ProfileCaptureProfile{{Kind: api.ProfileKindHeap, ProcessID: "7", Bytes: 4}}}
			blobs := []state.ProfileCaptureBlob{{Kind: api.ProfileKindHeap, ProcessID: "7", Profile: []byte("heap")}}
			if err := store.FinishProfileCapture(ctx, running, result, blobs, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			got, err = store.LatestOOMProfileCapture(ctx, acct.ID, app.ID, deployment)
			if err != nil || got.ID != running || !got.Partial || got.OOMKilledAt == nil || !got.OOMKilledAt.Equal(now) || got.OOMPeakMB != 263 || got.OOMPlanMB != 256 {
				t.Fatalf("OOM capture = %+v %v", got, err)
			}
			if _, err := store.LatestOOMProfileCapture(ctx, acct.ID, app.ID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("other deployment matched: %v", err)
			}
			other, err := store.CreateAccount(ctx, "profile-oom-other@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.LatestOOMProfileCapture(ctx, other.ID, app.ID, deployment); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign account read the OOM capture: %v", err)
			}

			// A slow_request capture is found by the trace id of the request
			// it was taken for.
			slow := api.ProfileCapture{ID: uuid.NewString(), Kinds: []string{api.ProfileKindStacks, api.ProfileKindCPU}, DurationSeconds: 5,
				Trigger: api.ProfileCaptureTriggerSlowRequest, TriggerDetail: "GET /orders in flight for 12s", RequestID: "req-1", RequestTraceID: "trace-1",
				CreatedAt: now, ExpiresAt: now.Add(api.ProfileCaptureRetention)}
			if err := store.QueueAutomaticProfileCapture(ctx, acct.ID, app.ID, slow, api.ProfileCaptureSlowRequestMinInterval, profileCaptureStorage); err != nil {
				t.Fatal(err)
			}
			got, err = store.ProfileCaptureForRequest(ctx, acct.ID, app.ID, "trace-1")
			if err != nil || got.ID != slow.ID || got.RequestID != "req-1" || got.RequestTraceID != "trace-1" || got.Trigger != api.ProfileCaptureTriggerSlowRequest {
				t.Fatalf("capture for request = %+v %v", got, err)
			}
			for _, tc := range []struct{ account, trace string }{{acct.ID, "trace-2"}, {acct.ID, ""}, {other.ID, "trace-1"}} {
				if _, err := store.ProfileCaptureForRequest(ctx, tc.account, app.ID, tc.trace); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("ProfileCaptureForRequest(%q) = %v, want not found", tc.trace, err)
				}
			}
		})
	}
}
