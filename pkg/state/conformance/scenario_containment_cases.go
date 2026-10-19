package conformance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261017201005: namespace cleanup cannot release a rehearsal app's
// retained network containment or turn captured work into outbound delivery.
func testScenarioRetainedContainment(t *testing.T, fx *Fixture) {
	s, ctx := fx.Store, fx.Ctx
	limits := api.MustLimitsFor(api.PlanPro)
	expiry := time.Now().UTC().Add(time.Hour)
	app, err := s.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: fx.Account.ID, Slug: "contained-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, Runtime: "node22", RAMMB: limits.RAMMB,
		MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
		PreviewOfSlug: fx.App.Slug, PreviewPrState: state.PreviewPrStateOpen,
		PreviewExpiresAt: &expiry,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	newHook := func(appID string) state.AppWebhook {
		t.Helper()
		hook, err := s.CreateAppWebhook(ctx, state.AppWebhook{
			AppID: appID, AccountID: fx.Account.ID,
			TargetURL: "https://example.test/capture", SecretSealed: []byte("sealed"),
			EventFilter: []string{string(state.AppWebhookEventAppDeployed)}, Enabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return hook
	}
	capturedHook, ordinaryHook := newHook(app.ID), newHook(fx.App.ID)
	missingApp, missingHook := uuid.NewString(), uuid.NewString()
	if run, err := s.ScenarioRehearsalNetworkRun(ctx, app.ID); err != nil || run != "" {
		t.Fatalf("unenrolled network = %q, %v; want no containment", run, err)
	}
	if blocked, err := s.ScenarioWebhookDeliveryBlocked(ctx, app.ID, capturedHook.ID); err != nil || blocked {
		t.Fatalf("unenrolled delivery blocked = %t, %v; want false", blocked, err)
	}
	runID := strings.Repeat("b", 32)
	if err := s.RegisterScenarioTestMembers(ctx, fx.Account.ID, runID, []state.ScenarioTestMember{{
		AppID: app.ID, Workload: "primary", NetworkMode: state.ScenarioNetworkContained,
		WebhookMode: state.ScenarioWebhookCaptureMode,
	}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, appID, hookID string
		blocked             bool
	}{
		{"captured sender", app.ID, ordinaryHook.ID, true},
		{"captured receiver", fx.App.ID, capturedHook.ID, true},
		{"canonical sender", strings.ToUpper(app.ID), missingHook, true},
		{"canonical receiver", missingApp, strings.ToUpper(capturedHook.ID), true},
		{"ordinary delivery", fx.App.ID, ordinaryHook.ID, false},
		{"missing identities", missingApp, missingHook, false},
	} {
		if blocked, err := s.ScenarioWebhookDeliveryBlocked(ctx, tc.appID, tc.hookID); err != nil || blocked != tc.blocked {
			t.Fatalf("%s blocked = %t, %v; want %t", tc.name, blocked, err, tc.blocked)
		}
	}
	assertNetwork := func(phase string) {
		t.Helper()
		for _, appID := range []string{app.ID, strings.ToUpper(app.ID)} {
			if run, err := s.ScenarioRehearsalNetworkRun(ctx, appID); err != nil || run != runID {
				t.Fatalf("%s containment = %q, %v; want %q", phase, run, err, runID)
			}
		}
		for _, appID := range []string{fx.App.ID, missingApp} {
			if run, err := s.ScenarioRehearsalNetworkRun(ctx, appID); err != nil || run != "" {
				t.Fatalf("%s ordinary/missing containment = %q, %v; want empty", phase, run, err)
			}
		}
	}
	assertNetwork("enrolled")
	if _, err := s.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteScenarioTestMembers(ctx, fx.Account.ID, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScenarioTestMemberByApp(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("namespace member after cleanup = %v; want ErrNotFound", err)
	}
	assertNetwork("after cleanup")
	if blocked, err := s.ScenarioWebhookDeliveryBlocked(ctx, app.ID, missingHook); err != nil || !blocked {
		t.Fatalf("retained capture authority = %t, %v; want true", blocked, err)
	}
	if blocked, err := s.ScenarioWebhookDeliveryBlocked(ctx, fx.App.ID, ordinaryHook.ID); err != nil || blocked {
		t.Fatalf("ordinary delivery after cleanup blocked = %t, %v; want false", blocked, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ScenarioRehearsalNetworkRun(canceled, app.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled containment read = %v; want context.Canceled", err)
	}
	if _, err := s.ScenarioWebhookDeliveryBlocked(canceled, app.ID, capturedHook.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture read = %v; want context.Canceled", err)
	}
}
