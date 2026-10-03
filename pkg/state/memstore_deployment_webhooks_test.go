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
	accountHook := seedMemAccountReleaseHook(m, account.ID,
		[]string{string(AppWebhookEventDeploymentLive), string(AppWebhookEventDeploymentFailed)}, true)
	liveOnlyAccountHook := seedMemAccountReleaseHook(m, account.ID,
		[]string{string(AppWebhookEventDeploymentLive)}, true)
	disabledAccountHook := seedMemAccountReleaseHook(m, account.ID,
		[]string{string(AppWebhookEventDeploymentLive)}, false)
	foreignAccountHook := seedMemAccountReleaseHook(m, otherAccount.ID,
		[]string{string(AppWebhookEventDeploymentLive)}, true)
	emptyFilterAccountHook := seedMemAccountReleaseHook(m, account.ID, nil, true)

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
	appDeliveries := assertDeliveryCount(allHook, 1)
	accountDeliveries := assertDeliveryCount(accountHook, 1)
	if appDeliveries[0].ID == accountDeliveries[0].ID || accountDeliveries[0].AppID != app.ID || accountDeliveries[0].AccountID != account.ID {
		t.Fatalf("app and account receivers must have separate, source-owned deliveries: app=%+v account=%+v", appDeliveries[0], accountDeliveries[0])
	}
	assertDeliveryCount(liveOnlyAccountHook, 1)
	assertDeliveryCount(disabledAccountHook, 0)
	assertDeliveryCount(foreignAccountHook, 0)
	assertDeliveryCount(emptyFilterAccountHook, 0)
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
	assertDeliveryCount(accountHook, 2)
	assertDeliveryCount(liveOnlyAccountHook, 1)
	assertDeliveryCount(disabledAccountHook, 0)
	assertDeliveryCount(foreignAccountHook, 0)
	assertDeliveryCount(emptyFilterAccountHook, 0)
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

	// A receiver registered before an app exists still sees that app's
	// release transitions without copying the subscription to the app.
	newApp, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "later-webhook-app", RAMMB: 512, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	newDeployment, err := m.CreateDeployment(ctx, Deployment{AppID: newApp.ID, ImageDigest: "sha256:later"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, newDeployment.ID); err != nil {
		t.Fatal(err)
	}
	fromNewApp, _, err := m.ListAppWebhookDeliveries(ctx, newApp.ID, accountHook.ID, 20, "")
	if err != nil || len(fromNewApp) != 1 || fromNewApp[0].AppID != newApp.ID || fromNewApp[0].Event != AppWebhookEventDeploymentLive {
		t.Fatalf("new app release deliveries = %+v, %v", fromNewApp, err)
	}
	assertDeliveryCount(allHook, 2)
}
