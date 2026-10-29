package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1155: automatic captures and captures linked to an OOM kill are
// announced through app webhooks, once per capture and event.
func TestProfileCaptureNotifications(t *testing.T) {
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
			acct, err := store.CreateAccount(ctx, "profile-notice@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "profile-notice", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			hooks := map[string]string{}
			for name, filter := range map[string][]string{"all": nil, "oom": {"profile.capture_oom_linked"}, "deploys": {"deployment.live"}} {
				h, err := store.CreateAppWebhook(ctx, state.AppWebhook{AppID: app.ID, AccountID: acct.ID, TargetURL: "https://" + name + ".example/hook", SecretSealed: []byte("sealed"), EventFilter: filter, Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				hooks[name] = h.ID
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			heap := []api.ProfileCaptureProfile{{Kind: api.ProfileKindHeap, ProcessID: "7", Bytes: 4}}
			// capture runs one capture to capturing on instance; finish, when
			// set, then stamps it ready with profiles.
			capture := func(trigger string, kinds []string, instance string, at time.Time, profiles []api.ProfileCaptureProfile, finish bool) string {
				t.Helper()
				c := api.ProfileCapture{ID: uuid.NewString(), Kinds: kinds, DurationSeconds: 10, Trigger: trigger, CreatedAt: at, ExpiresAt: at.Add(api.ProfileCaptureRetention)}
				if trigger == api.ProfileCaptureTriggerManual {
					err = store.CreateProfileCapture(ctx, acct.ID, app.ID, c, profileCaptureStorage)
				} else {
					err = store.QueueAutomaticProfileCapture(ctx, acct.ID, app.ID, c, time.Minute, profileCaptureStorage)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.ClaimProfileCapture(ctx, at); err != nil {
					t.Fatal(err)
				}
				if err := store.StartProfileCapture(ctx, c.ID, instance, "dep-1"); err != nil {
					t.Fatal(err)
				}
				if finish {
					finishNoticeCapture(ctx, t, store, c.ID, instance, profiles, at.Add(10*time.Second))
				}
				return c.ID
			}
			stamp := func(instance string, killedAt time.Time) {
				t.Helper()
				oom := state.ProfileCaptureOOM{KilledAt: killedAt, PeakMB: 263, PlanMB: 256}
				if _, err := store.StampProfileCaptureOOM(ctx, app.ID, instance, oom, killedAt.Add(-api.ProfileCaptureOOMLinkWindow)); err != nil {
					t.Fatal(err)
				}
			}

			// Not announced: a manual capture, and an automatic one without profiles.
			capture(api.ProfileCaptureTriggerManual, []string{api.ProfileKindHeap}, "i-manual", now.Add(-5*time.Minute), heap, true)
			capture(api.ProfileCaptureTriggerSlowRequest, []string{api.ProfileKindStacks}, "i-slow", now.Add(-4*time.Minute), nil, true)
			// Announced when ready, then again when a later OOM kill is linked;
			// a redelivered kill report adds nothing.
			pressure := capture(api.ProfileCaptureTriggerMemoryPressure, []string{api.ProfileKindHeap}, "i-pressure", now.Add(-3*time.Minute), heap, true)
			stamp("i-pressure", now.Add(-2*time.Minute))
			stamp("i-pressure", now.Add(-2*time.Minute))
			// A manual capture the kill interrupted is announced once, when it
			// finishes, with the kill.
			interrupted := capture(api.ProfileCaptureTriggerManual, []string{api.ProfileKindHeap}, "i-oom", now.Add(-time.Minute), heap, false)
			stamp("i-oom", now.Add(-50*time.Second))
			finishNoticeCapture(ctx, t, store, interrupted, "i-oom", heap, now.Add(-49*time.Second))

			relay, ok := store.(state.AppWebhookEventOutboxStore)
			if !ok {
				t.Fatal("store has no webhook outbox")
			}
			if _, err := relay.DrainAppWebhookEventOutbox(ctx, 32); err != nil {
				t.Fatal(err)
			}
			want := map[string][]string{
				"all":     {"profile.capture_ready " + pressure, "profile.capture_oom_linked " + pressure, "profile.capture_ready " + interrupted},
				"oom":     {"profile.capture_oom_linked " + pressure},
				"deploys": nil,
			}
			for name, id := range hooks {
				deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, id, 50, "")
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]api.ProfileCaptureWebhookPayload{}
				for _, d := range deliveries {
					var p api.ProfileCaptureWebhookPayload
					if err := json.Unmarshal(d.Payload, &p); err != nil {
						t.Fatal(err)
					}
					got[string(d.Event)+" "+p.Capture.ID] = p
				}
				if len(got) != len(want[name]) || len(deliveries) != len(want[name]) {
					t.Fatalf("%s hook got %d deliveries %v, want %v", name, len(deliveries), noticeKeys(got), want[name])
				}
				for _, key := range want[name] {
					p, ok := got[key]
					if !ok {
						t.Fatalf("%s hook missing %q; got %v", name, key, noticeKeys(got))
					}
					if p.Version != api.ProfileCaptureWebhookVersion || p.Capture.AppID != app.ID || p.Capture.Status != api.ProfileCaptureReady ||
						p.CapturePath != "/dashboard/apps/profile-notice/profiles/captures/"+p.Capture.ID || p.APIPath != "/v1/apps/profile-notice/profiles/captures/"+p.Capture.ID {
						t.Fatalf("%s payload = %+v", key, p)
					}
				}
			}
			for _, key := range []string{"profile.capture_oom_linked " + pressure, "profile.capture_ready " + interrupted} {
				deliveries, _, _ := store.ListAppWebhookDeliveries(ctx, app.ID, hooks["all"], 50, "")
				for _, d := range deliveries {
					var p api.ProfileCaptureWebhookPayload
					_ = json.Unmarshal(d.Payload, &p)
					if string(d.Event)+" "+p.Capture.ID == key && (p.Capture.OOMKilledAt == nil || p.Capture.OOMPeakMB != 263 || p.Capture.OOMPlanMB != 256) {
						t.Fatalf("%s lacks the OOM kill: %+v", key, p.Capture)
					}
				}
			}
		})
	}
}

func finishNoticeCapture(ctx context.Context, t *testing.T, store profileCaptureBackend, id, instance string, profiles []api.ProfileCaptureProfile, at time.Time) {
	t.Helper()
	result := api.ProfileCapture{Status: api.ProfileCaptureReady, InstanceID: instance, DeploymentID: "dep-1", Processes: 1, Profiles: profiles}
	var blobs []state.ProfileCaptureBlob
	for _, p := range profiles {
		blobs = append(blobs, state.ProfileCaptureBlob{Kind: p.Kind, ProcessID: p.ProcessID, Profile: []byte("prof")})
	}
	if err := store.FinishProfileCapture(ctx, id, result, blobs, at); err != nil {
		t.Fatal(err)
	}
}

func noticeKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
