// adr: 202610171904
package meter_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgResourceBillingSurvivesCreatorErasure(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	creator := makeBillableAccount(t, ctx, store, api.PlanPro)
	owner := makeAccount(t, ctx, store, api.PlanFree)
	org, err := store.CreateOrg(ctx, state.Org{Slug: "meter-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "meter-company-app", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	identity := state.BillingIdentity{AccountID: org.ID, Provider: "stripe", CustomerID: "cus_meter_company", SubscriptionID: "si_meter_company", BillingFrom: time.Now().UTC().Add(-time.Hour)}
	if err := store.UpsertBillingIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, identity.CustomerID); err != nil {
		t.Fatal(err)
	}
	saved, err := store.BillingIdentity(ctx, org.ID, "stripe")
	if err != nil || !saved.BillingFrom.Equal(identity.BillingFrom) {
		t.Fatalf("company identity update changed billing start: %+v %v", saved, err)
	}
	human, err := store.AccountByID(ctx, creator.ID)
	if err != nil || human.ProviderCustomerID != creator.ProviderCustomerID || human.StripeSubscriptionItem != creator.StripeSubscriptionItem {
		t.Fatalf("company adoption changed personal billing: %+v %v", human, err)
	}
	if err := store.TransferOrgOwnership(ctx, org.ID, creator.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	hour := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	const quantity = int64(123456)
	if err := store.AppendUsage(ctx, app.AccountID, app.ID, "company-instance", hour, quantity, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	now := hour.Add(time.Hour)
	completeUsageThrough(t, ctx, store, now)
	retryErr := errors.New("temporary provider failure")
	provider := &recordingStripe{err: retryErr}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if n, err := pusher.PushPending(ctx, 24*time.Hour); n != 0 || !errors.Is(err, retryErr) {
		t.Fatalf("failed company delivery = %d %v", n, err)
	}
	provider.err = nil
	if n, err := pusher.PushPending(ctx, 24*time.Hour); n != 1 || err != nil {
		t.Fatalf("company delivery retry = %d %v", n, err)
	}
	if n, err := pusher.PushPending(ctx, 24*time.Hour); n != 0 || err != nil {
		t.Fatalf("delivered company window replay = %d %v", n, err)
	}
	if calls := provider.Calls(); len(calls) != 2 || calls[0].AccountID != org.ID || calls[1].AccountID != org.ID || calls[1].MBSeconds != quantity {
		t.Fatalf("company usage reached the wrong financial subject: %+v", calls)
	}
}
