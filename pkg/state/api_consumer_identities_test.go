package state_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkAPIConsumerIdentities runs the ADR-20261124094745 store contract:
// OAuth callers enroll only while the app opts in, once per issuer and
// subject, onto the free plan, within the daily sign-up limit; a free-plan
// sign-up completes without a checkout.
func checkAPIConsumerIdentities(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	signups := st.(state.APIConsumerSignupStore)
	identities := st.(state.APIConsumerIdentityStore)
	plans := st.(state.APIConsumerPlanStore)
	free, err := plans.CreateAPIConsumerPlan(ctx, state.APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "free"})
	if err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Truncate(time.Minute)
	dayStart := minute.Truncate(24 * time.Hour)
	enroll := func(subject string) (state.APIConsumerIdentity, state.APIConsumer, bool, error) {
		return identities.EnrollAPIConsumerIdentity(ctx, state.APIConsumerEnrollment{AccountID: accountID, AppID: appID,
			Issuer: "https://idp.example/", Subject: subject, Email: "ada@example.com", EffectiveFrom: minute, DayStart: dayStart})
	}
	if _, _, _, err := enroll("user-1"); !errors.Is(err, state.ErrAPIConsumerOAuthOff) {
		t.Fatalf("enroll without sign-up settings: %v", err)
	}
	settings := state.APIConsumerSignupSettings{AppID: appID, AccountID: accountID, Enabled: true, Provider: state.APIConsumerSignupProviderStripe,
		PlanIDs: []string{free.ID}, KeyScopes: []string{"write"}, DisplayName: "Tools", DailyLimit: 2, FreePlanID: free.ID}
	if _, err := signups.PutAPIConsumerSignupSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := enroll("user-1"); !errors.Is(err, state.ErrAPIConsumerOAuthOff) {
		t.Fatalf("enroll with OAuth consumers off: %v", err)
	}
	settings.OAuthConsumers, settings.FreePlanID = true, ""
	if _, err := signups.PutAPIConsumerSignupSettings(ctx, settings); err == nil {
		t.Fatal("OAuth consumers without a free plan accepted")
	}
	settings.FreePlanID = free.ID
	if got, err := signups.PutAPIConsumerSignupSettings(ctx, settings); err != nil || !got.OAuthConsumers || got.FreePlanID != free.ID {
		t.Fatalf("put settings = %+v, %v", got, err)
	}

	id, consumer, created, err := enroll("user-1")
	if err != nil || !created || id.ConsumerID != consumer.ID || consumer.Name != "ada@example.com" || !strings.HasPrefix(consumer.ExternalRef, "oauth:") {
		t.Fatalf("first enrollment = %+v %+v %v %v", id, consumer, created, err)
	}
	if assigned, err := plans.ListAPIConsumerPlanAssignments(ctx, accountID, appID, consumer.ID); err != nil || len(assigned) != 1 ||
		assigned[0].PlanID != free.ID || !assigned[0].EffectiveFrom.Equal(minute) {
		t.Fatalf("enrolled consumer plan = %+v, %v", assigned, err)
	}
	again, same, created, err := enroll("user-1")
	if err != nil || created || same.ID != consumer.ID || again.ID != id.ID {
		t.Fatalf("second enrollment = %+v %+v %v %v", again, same, created, err)
	}
	if _, other, created, err := enroll("user-2"); err != nil || !created || other.ID == consumer.ID {
		t.Fatalf("another subject = %+v %v %v", other, created, err)
	}
	if _, _, _, err := enroll("user-3"); !errors.Is(err, state.ErrAPIConsumerSignupLimit) {
		t.Fatalf("third enrollment of the day: %v, want the daily limit", err)
	}

	// A free-plan sign-up completes without a checkout; a paid one does not.
	settings.DailyLimit = 10
	if _, err := signups.PutAPIConsumerSignupSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	signup, err := signups.CreateAPIConsumerSignup(ctx, state.APIConsumerSignup{AccountID: accountID, AppID: appID, Email: "bob@example.com",
		Name: "Bob", PlanID: free.ID, Provider: state.APIConsumerSignupProviderStripe, ClaimHash: bytes.Repeat([]byte{9}, 32),
		ExpiresAt: time.Now().Add(time.Hour)}, dayStart, 10)
	if err != nil {
		t.Fatal(err)
	}
	completion := state.APIConsumerSignupCompletion{AccountID: accountID, SignupID: signup.ID, EffectiveFrom: minute}
	if _, _, err := signups.CompleteAPIConsumerSignup(ctx, completion); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("checkout-less completion without Free: %v", err)
	}
	completion.Free = true
	bob, created, err := signups.CompleteAPIConsumerSignup(ctx, completion)
	if err != nil || !created || bob.ExternalRef != "signup:"+signup.ID {
		t.Fatalf("free completion = %+v %v %v", bob, created, err)
	}
	paid, err := plans.CreateAPIConsumerPlan(ctx, state.APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	paidSignup, err := signups.CreateAPIConsumerSignup(ctx, state.APIConsumerSignup{AccountID: accountID, AppID: appID, Email: "eve@example.com",
		Name: "Eve", PlanID: paid.ID, Provider: state.APIConsumerSignupProviderStripe, ClaimHash: bytes.Repeat([]byte{8}, 32),
		ExpiresAt: time.Now().Add(time.Hour)}, dayStart, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := signups.CompleteAPIConsumerSignup(ctx, state.APIConsumerSignupCompletion{AccountID: accountID, SignupID: paidSignup.ID,
		EffectiveFrom: minute, Free: true}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("a paid plan completed without a checkout: %v", err)
	}
}

// adr: 20261124094745
func TestMemStoreAPIConsumerIdentities(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkAPIConsumerIdentities(t, ctx, store, accountID, appID)
}

// adr: 20261124094745
func TestPgStoreAPIConsumerIdentities(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkAPIConsumerIdentities(t, ctx, store, accountID, appID)
}
