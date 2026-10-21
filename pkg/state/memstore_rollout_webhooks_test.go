// ADR-076: release outcome fan-out shares the durable webhook delivery ledger.
package state_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestMemStoreRolloutOutcomeWebhooks(t *testing.T) {
	m, ctx, account, app := state.WebhookFixture(t)
	completedHook := state.MemSampleWebhook(account.ID, app.ID)
	completedHook.EventFilter = []string{string(state.AppWebhookEventRolloutCompleted)}
	completedHook, err := m.CreateAppWebhook(ctx, completedHook)
	if err != nil {
		t.Fatal(err)
	}
	allHook := state.MemSampleWebhook(account.ID, app.ID)
	allHook.EventFilter = nil
	allHook, err = m.CreateAppWebhook(ctx, allHook)
	if err != nil {
		t.Fatal(err)
	}
	otherAccount, err := m.CreateAccount(ctx, "other-rollout-webhook@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	foreignHook := state.MemSampleWebhook(otherAccount.ID, app.ID)
	foreignHook, err = m.CreateAppWebhook(ctx, foreignHook)
	if err != nil {
		t.Fatal(err)
	}
	accountHook := state.SeedMemAccountReleaseHook(m, account.ID,
		[]string{string(state.AppWebhookEventRolloutCompleted), string(state.AppWebhookEventRolloutAborted)}, true)
	completedOnlyAccountHook := state.SeedMemAccountReleaseHook(m, account.ID,
		[]string{string(state.AppWebhookEventRolloutCompleted)}, true)
	disabledAccountHook := state.SeedMemAccountReleaseHook(m, account.ID,
		[]string{string(state.AppWebhookEventRolloutAborted)}, false)
	foreignAccountHook := state.SeedMemAccountReleaseHook(m, otherAccount.ID,
		[]string{string(state.AppWebhookEventRolloutAborted)}, true)
	emptyFilterAccountHook := state.SeedMemAccountReleaseHook(m, account.ID, nil, true)

	stable, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}

	canary, err := m.CreateDeployment(ctx, state.Deployment{
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
		hook state.AppWebhook
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
			if delivery.Event == state.AppWebhookEventRolloutCompleted || delivery.Event == state.AppWebhookEventRolloutAborted {
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
		if delivery.Event != state.AppWebhookEventRolloutCompleted && delivery.Event != state.AppWebhookEventRolloutAborted {
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
		case state.AppWebhookEventRolloutCompleted:
			if payload["deployment_id"] != stable.ID || payload["rollout_state"] != "complete" || payload["completed_at"] == nil {
				t.Errorf("completed payload = %v", payload)
			}
		case state.AppWebhookEventRolloutAborted:
			if payload["deployment_id"] != canary.ID || payload["reason"] != "manual stop" || payload["rollout_state"] != "aborted" || payload["aborted_at"] == nil {
				t.Errorf("aborted payload = %v", payload)
			}
		}
	}
}
