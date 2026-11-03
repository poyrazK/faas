// adr: 20261101143527 — the pusher bills no hour a subject spent on Free, and
// an organization being deleted still delivers the usage it ran before.
package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// A Pro customer cancels, runs on Free, then subscribes to Hobby inside the
// pusher's lookback. The hours on Free come up as pending once the subject is
// paid again; they settle with a zero receipt instead of billing as overage.
func TestPushPendingNeverBillsHoursSpentOnFree(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	day := month.AddDate(0, 0, 19)
	putComputeGrant(t, ctx, store, acct, api.PlanPro, month)
	putComputeGrant(t, ctx, store, acct, api.PlanFree, day.Add(2*time.Hour))
	putComputeGrant(t, ctx, store, acct, api.PlanHobby, day.Add(6*time.Hour+30*time.Minute))
	freeHour, paidHour := day.Add(3*time.Hour), day.Add(7*time.Hour)
	for i, use := range []struct {
		hour      time.Time
		mbSeconds int64
	}{{freeHour, 1_000}, {paidHour, 2_000}} {
		if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-"+string(rune('a'+i)), use.hour.Add(5*time.Minute), use.mbSeconds, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	now := day.Add(9 * time.Hour)
	completeUsageThrough(t, ctx, store, now)
	provider := &recordingStripe{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if n, err := pusher.PushPending(ctx, 24*time.Hour); n != 1 || err != nil {
		t.Fatalf("PushPending = %d, %v; want only the paid hour", n, err)
	}
	if calls := provider.Calls(); len(calls) != 1 || !calls[0].Hour.Equal(paidHour) || calls[0].MBSeconds != 2_000 {
		t.Fatalf("pushed %+v; want only %s's 2,000 mb_seconds", calls, paidHour)
	}
	// The Free hour was settled, so it does not come back.
	if n, err := pusher.PushPending(ctx, 24*time.Hour); n != 0 || err != nil || len(provider.Calls()) != 1 {
		t.Fatalf("replay = %d, %v, calls %+v", n, err, provider.Calls())
	}
}

// An organization marked for deletion still delivers the hours it ran before
// the request, as an account does; before, it delivered none of them.
func TestPushPendingDeliversADeletedOrganizationsEarlierUsage(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	creator := makeAccount(t, ctx, store, api.PlanFree)
	org, err := store.CreateOrg(ctx, state.Org{Slug: "deleted-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "deleted-company-app", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	identity := state.BillingIdentity{AccountID: org.ID, Provider: "stripe", CustomerID: "cus_deleted_company", SubscriptionID: "si_deleted_company", BillingFrom: time.Unix(0, 0).UTC()}
	if err := store.UpsertBillingIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, identity.CustomerID); err != nil {
		t.Fatal(err)
	}
	// Usage well before the deletion request, which is stamped now.
	hour := time.Now().UTC().Truncate(time.Hour).Add(-6 * time.Hour)
	if err := store.AppendUsage(ctx, app.AccountID, app.ID, "company-instance", hour.Add(5*time.Minute), 4_321, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.SoftDeleteOrg(ctx, org.ID); err != nil {
		t.Fatal(err)
	}
	subject, err := store.OrgResourceAccount(ctx, org.ID)
	if err != nil || subject.Status != state.AccountDeletedPending || subject.DeletionRequestedAt == nil {
		t.Fatalf("deleted organization subject = %+v, %v; want deleted_pending with its request time", subject, err)
	}
	now := hour.Add(2 * time.Hour)
	completeUsageThrough(t, ctx, store, now)
	provider := &recordingStripe{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if n, err := pusher.PushPending(ctx, 24*time.Hour); n != 1 || err != nil {
		t.Fatalf("PushPending = %d, %v; want the organization's hour", n, err)
	}
	if calls := provider.Calls(); len(calls) != 1 || calls[0].AccountID != org.ID || calls[0].MBSeconds != 4_321 {
		t.Fatalf("pushed %+v", calls)
	}
}
