// adr: 843
package state

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemEntityOutboxAcceptanceConcurrentRetryAndHistoryRetention(t *testing.T) {
	m, ctx, account, app := webhookFixture(t)
	hook, err := m.CreateAppWebhook(ctx, memSampleWebhook(account.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}
	in := AppWebhookDelivery{ID: uuid.NewString(), WebhookID: hook.ID, AppID: app.ID, AccountID: account.ID, Event: "reservation.confirmed", Payload: []byte(`{"reservation":"123"}`)}
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			if id, err := m.AcceptEntityOutboxDelivery(ctx, in); err != nil || id != in.ID {
				t.Errorf("acceptance = %s, %v", id, err)
			}
		})
	}
	group.Wait()
	rows, _, err := m.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 100, "")
	if err != nil || len(rows) != 1 || rows[0].ID != in.ID {
		t.Fatal(rows, err)
	}
	claimed := claimMemWebhookDelivery(t, m, ctx, in.ID)
	if err := m.MarkAppWebhookDeliverySucceeded(ctx, in.ID, 200, claimed.Attempt, claimed.NextAttemptAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AcceptEntityOutboxDelivery(ctx, in); err != nil {
		t.Fatal(err)
	}
	row, err := m.AppWebhookDeliveryByID(ctx, in.ID)
	if err != nil || row.Status != AppWebhookDeliverySucceeded || row.Attempt != claimed.Attempt {
		t.Fatal("retry reset terminal delivery", row, err)
	}
	if n, err := m.PruneAppWebhookDeliveries(ctx, time.Now().Add(time.Hour), 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := m.DeleteAppWebhook(ctx, hook.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := m.AcceptEntityOutboxDelivery(ctx, in); err != nil || id != in.ID {
		t.Fatal("receipt did not outlive history/destination", id, err)
	}
	if _, err := m.AppWebhookDeliveryByID(ctx, in.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("retained receipt recreated delivery", err)
	}
	for _, kind := range []string{"payload", "event", "account", "app", "webhook"} {
		changed := in
		switch kind {
		case "payload":
			changed.Payload = []byte(`{"reservation":"different"}`)
		case "event":
			changed.Event = "different"
		case "account":
			changed.AccountID = uuid.NewString()
		case "app":
			changed.AppID = uuid.NewString()
		case "webhook":
			changed.WebhookID = uuid.NewString()
		}
		if _, err := m.AcceptEntityOutboxDelivery(ctx, changed); !errors.Is(err, ErrConflict) {
			t.Fatal(kind, err)
		}
	}
}

func TestMemEntityOutboxRejectedDestinationLeavesNoAcceptanceReceipt(t *testing.T) {
	m, ctx, account, app := webhookFixture(t)
	hook, err := m.CreateAppWebhook(ctx, memSampleWebhook(account.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}
	in := AppWebhookDelivery{ID: uuid.NewString(), WebhookID: hook.ID, AppID: app.ID, AccountID: account.ID, Event: "reservation.confirmed", Payload: []byte(`null`)}
	for _, kind := range []string{"disabled", "account", "app", "scope", "missing"} {
		t.Run(kind, func(t *testing.T) {
			m.mu.Lock()
			changed := hook
			switch kind {
			case "disabled":
				changed.Enabled = false
			case "account":
				changed.AccountID = uuid.NewString()
			case "app":
				changed.AppID = uuid.NewString()
			case "scope":
				changed.Scope = AppWebhookScopePlatformTenant
			}
			m.appWebhooks[hook.ID] = changed
			if kind == "missing" {
				delete(m.appWebhooks, hook.ID)
			}
			m.mu.Unlock()
			if _, err := m.AcceptEntityOutboxDelivery(ctx, in); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			m.mu.Lock()
			receipts := len(m.entityOutboxAcceptances)
			m.appWebhooks[hook.ID] = hook
			m.mu.Unlock()
			if receipts != 0 {
				t.Fatal("rejected destination left acceptance evidence")
			}
		})
	}
	if _, err := m.AcceptEntityOutboxDelivery(ctx, in); err != nil {
		t.Fatal(err)
	}
	invalid := in
	invalid.ID = "invalid"
	if _, err := m.AcceptEntityOutboxDelivery(ctx, invalid); !errors.Is(err, ErrEntityOutboxInvalid) {
		t.Fatal(err)
	}
}

func TestMemEntityOutboxAcceptanceRechecksAdmissionAtInsert(t *testing.T) {
	for _, kind := range []string{"account", "abuse", "plan", "app", "workload"} {
		t.Run(kind, func(t *testing.T) {
			m, ctx, account, app := webhookFixture(t)
			hook, err := m.CreateAppWebhook(ctx, memSampleWebhook(account.ID, app.ID))
			if err != nil {
				t.Fatal(err)
			}
			in := AppWebhookDelivery{ID: uuid.NewString(), WebhookID: hook.ID, AppID: app.ID, AccountID: account.ID, Event: "reservation.confirmed", Payload: []byte(`null`)}
			m.mu.Lock()
			changedAccount, changedApp := account, app
			switch kind {
			case "account":
				changedAccount.Status = AccountSuspended
			case "abuse":
				at := time.Now()
				changedAccount.AbuseHoldAt = &at
			case "plan":
				changedAccount.Plan = api.PlanFree
			case "app":
				changedApp.Status = AppDeleted
			case "workload":
				changedApp.WorkloadClass = WorkloadClassWorker
			}
			m.accounts[account.ID], m.apps[app.ID] = changedAccount, changedApp
			m.mu.Unlock()
			if _, err := m.AcceptEntityOutboxDelivery(ctx, in); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			m.mu.Lock()
			receipts := len(m.entityOutboxAcceptances)
			m.accounts[account.ID], m.apps[app.ID] = account, app
			m.mu.Unlock()
			if receipts != 0 {
				t.Fatal("admission refusal retained acceptance evidence")
			}
			if _, err := m.AcceptEntityOutboxDelivery(ctx, in); err != nil {
				t.Fatal(err)
			}
		})
	}
}
