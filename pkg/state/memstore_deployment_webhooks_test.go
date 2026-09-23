// ADR-076: test only deployment lifecycle rows now that all-events hooks also
// receive release outcome rows.
package state

import (
	"encoding/json"
	"testing"
)

func TestMemStoreDeploymentLifecycleWebhooks(t *testing.T) {
	m, ctx, account, app := webhookFixture(t)
	liveHook := memSampleWebhook(account.ID, app.ID)
	liveHook.EventFilter = []string{string(AppWebhookEventDeploymentLive)}
	liveHook, err := m.CreateAppWebhook(ctx, liveHook)
	if err != nil {
		t.Fatal(err)
	}
	allHook := memSampleWebhook(account.ID, app.ID)
	allHook.EventFilter = nil
	allHook, err = m.CreateAppWebhook(ctx, allHook)
	if err != nil {
		t.Fatal(err)
	}
	otherAccount, err := m.CreateAccount(ctx, "other-deployment-webhook@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	foreignHook := memSampleWebhook(otherAccount.ID, app.ID)
	foreignHook.EventFilter = nil
	foreignHook, err = m.CreateAppWebhook(ctx, foreignHook)
	if err != nil {
		t.Fatal(err)
	}

	live, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:live"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	assertDeliveryCount := func(hook AppWebhook, want int) []AppWebhookDelivery {
		t.Helper()
		got, _, err := m.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 20, "")
		if err != nil {
			t.Fatal(err)
		}
		var lifecycle []AppWebhookDelivery
		for _, delivery := range got {
			if delivery.Event == AppWebhookEventDeploymentLive || delivery.Event == AppWebhookEventDeploymentFailed {
				lifecycle = append(lifecycle, delivery)
			}
		}
		if len(lifecycle) != want {
			t.Fatalf("hook %s deployment deliveries = %d, want %d", hook.ID, len(lifecycle), want)
		}
		return lifecycle
	}
	assertDeliveryCount(liveHook, 1)
	assertDeliveryCount(allHook, 1)
	assertDeliveryCount(foreignHook, 0)

	failed, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:failed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDeploymentFailedEx(ctx, failed.ID, "build_timeout", "build failed", "Try a smaller build", "The build timed out", "Reduce dependencies", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDeploymentFailedEx(ctx, failed.ID, "build_timeout", "build failed", "Try a smaller build", "The build timed out", "Reduce dependencies", nil); err != nil {
		t.Fatal(err)
	}
	assertDeliveryCount(liveHook, 1)
	deliveries := assertDeliveryCount(allHook, 2)
	assertDeliveryCount(foreignHook, 0)
	for _, delivery := range deliveries {
		var payload map[string]any
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["app_id"] != app.ID || payload["status"] != string(delivery.Event)[len("deployment."):] {
			t.Fatalf("unexpected %s payload: %v", delivery.Event, payload)
		}
		if delivery.Event == AppWebhookEventDeploymentFailed &&
			(payload["error_code"] != "build_timeout" || payload["error_hint"] != "Try a smaller build") {
			t.Fatalf("failed payload lacks explanation: %v", payload)
		}
	}
}
