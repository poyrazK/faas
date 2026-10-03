// ADR-076: release outcome fan-out shares the durable webhook delivery ledger.
package state

import (
	"encoding/json"
	"testing"
)

func TestMemStoreRolloutOutcomeWebhooks(t *testing.T) {
	m, ctx, account, app := webhookFixture(t)
	completedHook := memSampleWebhook(account.ID, app.ID)
	completedHook.EventFilter = []string{string(AppWebhookEventRolloutCompleted)}
	completedHook, err := m.CreateAppWebhook(ctx, completedHook)
	if err != nil {
		t.Fatal(err)
	}
	allHook := memSampleWebhook(account.ID, app.ID)
	allHook.EventFilter = nil
	allHook, err = m.CreateAppWebhook(ctx, allHook)
	if err != nil {
		t.Fatal(err)
	}
	otherAccount, err := m.CreateAccount(ctx, "other-rollout-webhook@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	foreignHook := memSampleWebhook(otherAccount.ID, app.ID)
	foreignHook, err = m.CreateAppWebhook(ctx, foreignHook)
	if err != nil {
		t.Fatal(err)
	}
	accountHook := seedMemAccountReleaseHook(m, account.ID,
		[]string{string(AppWebhookEventRolloutCompleted), string(AppWebhookEventRolloutAborted)}, true)
	completedOnlyAccountHook := seedMemAccountReleaseHook(m, account.ID,
		[]string{string(AppWebhookEventRolloutCompleted)}, true)
	disabledAccountHook := seedMemAccountReleaseHook(m, account.ID,
		[]string{string(AppWebhookEventRolloutAborted)}, false)
	foreignAccountHook := seedMemAccountReleaseHook(m, otherAccount.ID,
		[]string{string(AppWebhookEventRolloutAborted)}, true)
	emptyFilterAccountHook := seedMemAccountReleaseHook(m, account.ID, nil, true)

	stable, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}

	canary, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:canary", CanaryPreset: "balanced",
		CanaryTotalSteps: 2, TrafficPercent: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, canary.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.RecoverRollout(ctx, app.ID, "abort", "manual stop"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		hook AppWebhook
		want int
	}{
		{completedHook, 1}, {allHook, 2}, {foreignHook, 0},
		{accountHook, 2}, {completedOnlyAccountHook, 1},
		{disabledAccountHook, 0}, {foreignAccountHook, 0}, {emptyFilterAccountHook, 0},
	} {
		got, _, err := m.ListAppWebhookDeliveries(ctx, app.ID, tc.hook.ID, 20, "")
		if err != nil {
			t.Fatal(err)
		}
		rolloutCount := 0
		for _, delivery := range got {
			if delivery.Event == AppWebhookEventRolloutCompleted || delivery.Event == AppWebhookEventRolloutAborted {
				rolloutCount++
			}
		}
		if rolloutCount != tc.want {
			t.Errorf("hook %s rollout deliveries = %d, want %d", tc.hook.ID, rolloutCount, tc.want)
		}
	}
	accountDeliveries, _, err := m.ListAppWebhookDeliveries(ctx, app.ID, accountHook.ID, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range accountDeliveries {
		if delivery.AppID != app.ID || delivery.AccountID != account.ID {
			t.Errorf("account delivery has wrong source: %+v", delivery)
		}
	}

	got, _, err := m.ListAppWebhookDeliveries(ctx, app.ID, allHook.ID, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range got {
		if delivery.Event != AppWebhookEventRolloutCompleted && delivery.Event != AppWebhookEventRolloutAborted {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["app_id"] != app.ID {
			t.Errorf("wrong app in %s: %v", delivery.Event, payload)
		}
		switch delivery.Event {
		case AppWebhookEventRolloutCompleted:
			if payload["deployment_id"] != stable.ID || payload["rollout_state"] != "complete" || payload["completed_at"] == nil {
				t.Errorf("completed payload = %v", payload)
			}
		case AppWebhookEventRolloutAborted:
			if payload["deployment_id"] != canary.ID || payload["reason"] != "manual stop" || payload["rollout_state"] != "aborted" || payload["aborted_at"] == nil {
				t.Errorf("aborted payload = %v", payload)
			}
		}
	}
}
