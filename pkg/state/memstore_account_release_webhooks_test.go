package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreAccountReleaseWebhook_PaginationAndScope(t *testing.T) {
	m, ctx, account, app := webhookFixture(t)
	hook, err := m.CreateAccountReleaseWebhookIfUnderQuota(ctx, AppWebhook{
		AccountID: account.ID, Scope: AppWebhookScopeAccount,
		TargetURL: "https://example.com/releases", SecretSealed: []byte("sealed"),
		EventFilter: []string{"deployment.live"}, Enabled: true,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		_, err := m.RecordAppWebhookDelivery(ctx, AppWebhookDelivery{
			WebhookID: hook.ID, AppID: app.ID, AccountID: account.ID,
			Event: AppWebhookEventDeploymentLive, Payload: []byte(`{}`),
			CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, next, err := m.ListAccountReleaseWebhookDeliveries(ctx, account.ID, hook.ID, 1, "")
	if err != nil || len(first) != 1 || next == "" {
		t.Fatalf("first page=%d next=%q err=%v", len(first), next, err)
	}
	second, end, err := m.ListAccountReleaseWebhookDeliveries(ctx, account.ID, hook.ID, 1, next)
	if err != nil || len(second) != 1 || end != "" || first[0].ID == second[0].ID {
		t.Fatalf("second page=%v end=%q err=%v", second, end, err)
	}
	foreign, _, err := m.ListAccountReleaseWebhookDeliveries(ctx, "other-account", hook.ID, 10, "")
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign page=%d err=%v", len(foreign), err)
	}
}
