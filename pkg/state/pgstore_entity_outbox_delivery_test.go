// adr: 933
package state_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgEntityOutboxAcceptanceConcurrentRestartAndRetention(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	hook, err := store.CreateAppWebhook(ctx, pgSampleWebhook(accountID, appID))
	if err != nil {
		t.Fatal(err)
	}
	in := state.AppWebhookDelivery{ID: uuid.NewString(), WebhookID: hook.ID, AppID: appID, AccountID: accountID, Event: "reservation.confirmed", Payload: []byte(`{"reservation":"123"}`)}
	restarted := state.NewPgStore(pool)
	var group sync.WaitGroup
	for _, relay := range []*state.PgStore{store, restarted} {
		for range 4 {
			group.Go(func() {
				if id, err := relay.AcceptEntityOutboxDelivery(ctx, in); err != nil || id != in.ID {
					t.Errorf("acceptance = %s, %v", id, err)
				}
			})
		}
	}
	group.Wait()
	rows, _, err := store.ListAppWebhookDeliveries(ctx, appID, hook.ID, 100, "")
	if err != nil || len(rows) != 1 || rows[0].ID != in.ID {
		t.Fatal(rows, err)
	}
	claimed := claimPgWebhookDelivery(t, store, ctx, in.ID)
	if err := store.MarkAppWebhookDeliverySucceeded(ctx, in.ID, 200, claimed.Attempt, claimed.NextAttemptAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.AcceptEntityOutboxDelivery(ctx, in); err != nil {
		t.Fatal(err)
	}
	row, err := store.AppWebhookDeliveryByID(ctx, in.ID)
	if err != nil || row.Status != state.AppWebhookDeliverySucceeded || row.Attempt != claimed.Attempt+1 {
		t.Fatal(row, err)
	}
	if _, err := store.PruneAppWebhookDeliveries(ctx, time.Now().Add(time.Hour), 1000); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppWebhook(ctx, hook.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := restarted.AcceptEntityOutboxDelivery(ctx, in); err != nil || id != in.ID {
		t.Fatal("acceptance lost with history", id, err)
	}
	if _, err := store.AppWebhookDeliveryByID(ctx, in.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("retry recreated delivery", err)
	}
	in.Payload = []byte(`{"reservation":"different"}`)
	if _, err := restarted.AcceptEntityOutboxDelivery(ctx, in); !errors.Is(err, state.ErrConflict) {
		t.Fatal("identity conflict accepted", err)
	}
}

func TestPgEntityOutboxMissingDestinationRollsBackReceipt(t *testing.T) {
	store, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	hook, err := store.CreateAppWebhook(ctx, pgSampleWebhook(accountID, appID))
	if err != nil {
		t.Fatal(err)
	}
	in := state.AppWebhookDelivery{ID: uuid.NewString(), WebhookID: uuid.NewString(), AppID: appID, AccountID: accountID, Event: "reservation.confirmed", Payload: []byte(`null`)}
	if _, err := store.AcceptEntityOutboxDelivery(ctx, in); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	// Changing the destination would conflict if the rejected attempt left a receipt.
	in.WebhookID = hook.ID
	if id, err := store.AcceptEntityOutboxDelivery(ctx, in); err != nil || id != in.ID {
		t.Fatal(id, err)
	}
}

func TestPgEntityOutboxAdmissionRefusalRollsBackReceipt(t *testing.T) {
	store, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	hook, err := store.CreateAppWebhook(ctx, pgSampleWebhook(accountID, appID))
	if err != nil {
		t.Fatal(err)
	}
	in := state.AppWebhookDelivery{ID: uuid.NewString(), WebhookID: hook.ID, AppID: appID, AccountID: accountID, Event: "reservation.confirmed", Payload: []byte(`null`)}
	if err := store.UpdateAccountStatus(ctx, accountID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcceptEntityOutboxDelivery(ctx, in); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStatus(ctx, accountID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcceptEntityOutboxDelivery(ctx, in); err != nil {
		t.Fatal("refusal did not roll back receipt", err)
	}
}
