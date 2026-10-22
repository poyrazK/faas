package state_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkAPIConsumerSignups runs the ADR-20261021143000 store contract:
// settings scope sign-ups to an app, the daily limit holds, completion
// creates the consumer with its plan and trial once, and the last issued
// key is tracked.
func checkAPIConsumerSignups(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	signups := st.(state.APIConsumerSignupStore)
	plan, err := st.(state.APIConsumerPlanStore).CreateAPIConsumerPlan(ctx, state.APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	settings := state.APIConsumerSignupSettings{AppID: appID, AccountID: accountID, Enabled: true, Provider: state.APIConsumerSignupProviderStripe,
		PlanIDs: []string{plan.ID}, KeyScopes: []string{"write", "billing"}, DisplayName: "Weather API", TermsURL: "https://example.com/terms",
		TrialDays: 14, DailyLimit: 2}
	put, err := signups.PutAPIConsumerSignupSettings(ctx, settings)
	if err != nil || put.DisplayName != "Weather API" || len(put.PlanIDs) != 1 || put.PlanIDs[0] != plan.ID || put.DailyLimit != 2 {
		t.Fatalf("put settings = %+v, %v", put, err)
	}
	if _, err := signups.PutAPIConsumerSignupSettings(ctx, state.APIConsumerSignupSettings{AppID: uuid.NewString(), AccountID: accountID,
		Provider: state.APIConsumerSignupProviderStripe, KeyScopes: []string{"read"}, DisplayName: "x", DailyLimit: 1}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("settings for an unknown app: %v", err)
	}
	if got, err := signups.GetAPIConsumerSignupSettings(ctx, accountID, appID); err != nil || got.TrialDays != 14 || len(got.KeyScopes) != 2 {
		t.Fatalf("get settings = %+v, %v", got, err)
	}

	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	claim := bytes.Repeat([]byte{7}, 32)
	create := func(email string) (state.APIConsumerSignup, error) {
		return signups.CreateAPIConsumerSignup(ctx, state.APIConsumerSignup{AccountID: accountID, AppID: appID, Email: email, Name: "Ada",
			PlanID: plan.ID, Provider: state.APIConsumerSignupProviderStripe, ClaimHash: claim, ExpiresAt: time.Now().Add(24 * time.Hour)}, dayStart, 2)
	}
	first, err := create("ada@example.com")
	if err != nil || first.Status != state.APIConsumerSignupPending || first.PlanID != plan.ID || !bytes.Equal(first.ClaimHash, claim) {
		t.Fatalf("first sign-up = %+v, %v", first, err)
	}
	second, err := create("bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := create("eve@example.com"); !errors.Is(err, state.ErrAPIConsumerSignupLimit) {
		t.Fatalf("third sign-up of the day: %v, want the daily limit", err)
	}

	if _, _, err := signups.CompleteAPIConsumerSignup(ctx, state.APIConsumerSignupCompletion{AccountID: accountID, SignupID: second.ID,
		EffectiveFrom: time.Now().UTC().Truncate(time.Minute)}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("completing before checkout: %v, want conflict", err)
	}
	customer := "cus_" + uuid.NewString()[:8]
	if err := signups.SetAPIConsumerSignupCheckout(ctx, accountID, first.ID, customer, "cs_test_1"); err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Truncate(time.Minute)
	completion := state.APIConsumerSignupCompletion{AccountID: accountID, SignupID: first.ID, EffectiveFrom: minute, TrialDays: 14}
	consumer, created, err := signups.CompleteAPIConsumerSignup(ctx, completion)
	if err != nil || !created || consumer.ExternalRef != customer || consumer.Name != "Ada" || consumer.AppID != appID {
		t.Fatalf("complete = %+v, %t, %v", consumer, created, err)
	}
	again, created, err := signups.CompleteAPIConsumerSignup(ctx, completion)
	if err != nil || created || again.ID != consumer.ID {
		t.Fatalf("second completion = %+v, %t, %v; want the same consumer", again, created, err)
	}
	assignments, err := st.(state.APIConsumerPlanStore).ListAPIConsumerPlanAssignments(ctx, accountID, appID, consumer.ID)
	if err != nil || len(assignments) != 1 || assignments[0].PlanID != plan.ID || !assignments[0].EffectiveFrom.Equal(minute) {
		t.Fatalf("assignments = %+v, %v", assignments, err)
	}
	trial, err := st.(state.APIConsumerTrialStore).GetAPIConsumerTrial(ctx, accountID, appID, consumer.ID)
	if err != nil || !trial.StartsAt.Equal(minute) || !trial.EndsAt.Equal(minute.AddDate(0, 0, 14)) {
		t.Fatalf("trial = %+v, %v", trial, err)
	}
	done, err := signups.GetAPIConsumerSignup(ctx, accountID, first.ID)
	if err != nil || done.Status != state.APIConsumerSignupCompleted || done.ConsumerID != consumer.ID || done.CompletedAt == nil ||
		done.ProviderCustomerID != customer || done.CheckoutSessionID != "cs_test_1" {
		t.Fatalf("completed sign-up = %+v, %v", done, err)
	}
	if err := signups.SetAPIConsumerSignupCheckout(ctx, accountID, first.ID, "cus_other", "cs_other"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("checkout of a completed sign-up: %v", err)
	}

	keyA, keyB := uuid.NewString(), uuid.NewString()
	if prior, err := signups.SetAPIConsumerSignupKey(ctx, accountID, first.ID, keyA); err != nil || prior != "" {
		t.Fatalf("first key: %q, %v", prior, err)
	}
	if prior, err := signups.SetAPIConsumerSignupKey(ctx, accountID, first.ID, keyB); err != nil || prior != keyA {
		t.Fatalf("second key: %q, %v; want %s replaced", prior, err, keyA)
	}
	if _, err := signups.SetAPIConsumerSignupKey(ctx, accountID, uuid.NewString(), keyB); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("key of an unknown sign-up: %v", err)
	}

	list, err := signups.ListAPIConsumerSignups(ctx, accountID, appID, 10)
	if err != nil || len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if deleted, err := signups.DeleteStaleAPIConsumerSignups(ctx, time.Now().Add(48*time.Hour)); err != nil || deleted != 1 {
		t.Fatalf("stale deleted = %d, %v; want only the pending one", deleted, err)
	}
	if _, err := signups.GetAPIConsumerSignup(ctx, accountID, second.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale sign-up still readable: %v", err)
	}
	if err := signups.DeleteAPIConsumerSignupSettings(ctx, accountID, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := create("late@example.com"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("sign-up after the settings were removed: %v", err)
	}
}

// adr: 20261021143000
func TestMemStoreAPIConsumerSignups(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkAPIConsumerSignups(t, ctx, store, accountID, appID)
}

// adr: 20261021143000
func TestPgStoreAPIConsumerSignups(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkAPIConsumerSignups(t, ctx, store, accountID, appID)
}
