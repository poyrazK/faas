package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func checkAPIConsumerIdentityLinks(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	plans := st.(state.APIConsumerPlanStore)
	links := st.(state.APIConsumerIdentityLinkStore)
	free, err := plans.CreateAPIConsumerPlan(ctx, state.APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "free"})
	if err != nil {
		t.Fatal(err)
	}
	signups := st.(state.APIConsumerSignupStore)
	if _, err := signups.PutAPIConsumerSignupSettings(ctx, state.APIConsumerSignupSettings{AccountID: accountID, AppID: appID, Enabled: true,
		Provider: state.APIConsumerSignupProviderStripe, PlanIDs: []string{free.ID}, KeyScopes: []string{"write"}, DisplayName: "Tools",
		DailyLimit: 10, FreePlanID: free.ID, OAuthConsumers: true}); err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Truncate(time.Minute)
	_, enrolled, _, err := st.(state.APIConsumerIdentityStore).EnrollAPIConsumerIdentity(ctx, state.APIConsumerEnrollment{AccountID: accountID,
		AppID: appID, Issuer: "https://login.example", Subject: "u1", EffectiveFrom: minute, DayStart: minute.Truncate(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	upgrades := st.(state.APIConsumerUpgradeStore)
	if n, err := upgrades.CountCardlessAPIConsumersOnPlan(ctx, accountID, appID, free.ID, time.Now()); err != nil || n != 1 {
		t.Fatalf("card-less consumers on the free plan = %d %v; want 1", n, err)
	}
	keyed, err := st.CreateAPIConsumer(ctx, accountID, appID, "cus_keyed", "Ada")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateAPIConsumer(ctx, accountID, appID, "cus_other", "Bob")
	if err != nil {
		t.Fatal(err)
	}
	link := func(subject, consumerID string) (state.APIConsumerIdentity, string, error) {
		return links.LinkAPIConsumerIdentity(ctx, state.APIConsumerIdentityLink{AccountID: accountID, AppID: appID,
			Issuer: "https://login.example", Subject: subject, ConsumerID: consumerID})
	}

	// The enrolled consumer is revoked and the identity moves to the key's.
	id, revoked, err := link("u1", keyed.ID)
	if err != nil || id.ConsumerID != keyed.ID || revoked != enrolled.ID {
		t.Fatalf("link = %+v %q %v", id, revoked, err)
	}
	if c, _ := st.GetAPIConsumerByID(ctx, accountID, enrolled.ID); c.Active() {
		t.Fatal("the enrolled consumer is still active")
	}
	if n, err := upgrades.CountCardlessAPIConsumersOnPlan(ctx, accountID, appID, free.ID, time.Now()); err != nil || n != 0 {
		t.Fatalf("card-less consumers after the link closed one = %d %v; want 0", n, err)
	}
	if again, revoked, err := link("u1", keyed.ID); err != nil || again.ID != id.ID || revoked != "" {
		t.Fatalf("link again = %+v %q %v; want a no-op", again, revoked, err)
	}
	// Moving it to another key's consumer needs the owner.
	if _, _, err := link("u1", other.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("relink = %v; want conflict", err)
	}
	// An identity that never enrolled links directly.
	if fresh, revoked, err := link("u2", other.ID); err != nil || fresh.ConsumerID != other.ID || revoked != "" {
		t.Fatalf("fresh link = %+v %q %v", fresh, revoked, err)
	}
	if _, _, err := link("u3", "00000000-0000-4000-8000-000000000001"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown consumer = %v; want not found", err)
	}
	if _, err := st.RevokeAPIConsumer(ctx, accountID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := link("u4", other.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("revoked consumer = %v; want not found", err)
	}

	listed, err := links.ListAPIConsumerIdentities(ctx, accountID, appID)
	if err != nil || len(listed) != 2 {
		t.Fatalf("list = %+v %v", listed, err)
	}
	if err := links.DeleteAPIConsumerIdentity(ctx, accountID, appID, id.ID); err != nil {
		t.Fatal(err)
	}
	if err := links.DeleteAPIConsumerIdentity(ctx, accountID, appID, id.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delete again = %v; want not found", err)
	}
	// After the owner unlinks it, the identity links to another consumer.
	third, err := st.CreateAPIConsumer(ctx, accountID, appID, "cus_third", "Cy")
	if err != nil {
		t.Fatal(err)
	}
	if moved, _, err := link("u1", third.ID); err != nil || moved.ConsumerID != third.ID {
		t.Fatalf("link after unlink = %+v %v", moved, err)
	}
	// The consumer the link closed gave up its ref: after an unlink, the
	// identity enrolls a new consumer rather than the closed one.
	if c, _ := st.GetAPIConsumerByID(ctx, accountID, enrolled.ID); c.ExternalRef != "linked:"+enrolled.ID {
		t.Fatalf("closed consumer ref = %q", c.ExternalRef)
	}
	listed, _ = links.ListAPIConsumerIdentities(ctx, accountID, appID)
	for _, id := range listed {
		if id.Subject == "u1" {
			if err := links.DeleteAPIConsumerIdentity(ctx, accountID, appID, id.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, fresh, created, err := st.(state.APIConsumerIdentityStore).EnrollAPIConsumerIdentity(ctx, state.APIConsumerEnrollment{AccountID: accountID,
		AppID: appID, Issuer: "https://login.example", Subject: "u1", EffectiveFrom: minute, DayStart: minute.Truncate(24 * time.Hour)})
	if err != nil || !created || !fresh.Active() || fresh.ID == enrolled.ID {
		t.Fatalf("enroll after unlink = %+v %v %v; want a new consumer", fresh, created, err)
	}
}

// adr: 20261124094745
func TestMemStoreAPIConsumerIdentityLinks(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkAPIConsumerIdentityLinks(t, ctx, store, accountID, appID)
}

// adr: 20261124094745
func TestPgStoreAPIConsumerIdentityLinks(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkAPIConsumerIdentityLinks(t, ctx, store, accountID, appID)
}
