package state_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// consumerKeyQuotaContract issues an app's keys from concurrent callers
// against a cap of three per app: exactly three succeed, the others are
// refused, and a revoked key frees its slot.
func consumerKeyQuotaContract(t *testing.T, store state.Store) {
	t.Helper()
	ctx, suffix := t.Context(), uuid.NewString()[:8]
	acct, err := store.CreateAccount(ctx, "keys-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "keys-" + suffix, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, acct.ID, app.ID, "ref-"+suffix, "keys")
	if err != nil {
		t.Fatal(err)
	}
	quota := state.ConsumerKeyQuota{PerApp: 3, PerAccount: 100}
	issue := func(name string) (state.ConsumerKey, error) {
		_, prefix, hash, err := api.GenerateConsumerKey()
		if err != nil {
			return state.ConsumerKey{}, err
		}
		return store.CreateConsumerKeyForConsumerIfUnderQuota(ctx, acct.ID, consumer.ID, name, prefix, hash, []string{"read"}, nil, quota)
	}

	const callers = 12
	var wg sync.WaitGroup
	errs := make([]error, callers)
	keys := make([]state.ConsumerKey, callers)
	for i := range callers {
		wg.Go(func() { keys[i], errs[i] = issue(fmt.Sprintf("key-%d", i)) })
	}
	wg.Wait()
	var issued []state.ConsumerKey
	for i, err := range errs {
		var q *state.ConsumerKeyQuotaError
		switch {
		case err == nil:
			issued = append(issued, keys[i])
		case errors.As(err, &q) && q.Scope == api.PlanQuotaScopeApp && q.Limit == quota.PerApp && q.Observed == quota.PerApp:
		default:
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if len(issued) != quota.PerApp {
		t.Fatalf("%d concurrent callers were issued %d keys, want the cap of %d", callers, len(issued), quota.PerApp)
	}

	if _, err := issue("over"); !errors.As(err, new(*state.ConsumerKeyQuotaError)) {
		t.Fatalf("issue at the cap: %v, want a quota error", err)
	}
	if _, err := store.RevokeConsumerKey(ctx, acct.ID, issued[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := issue("after-revoke"); err != nil {
		t.Fatalf("issue after a revoke freed a slot: %v", err)
	}
	if _, err := store.CreateConsumerKeyForConsumerIfUnderQuota(ctx, uuid.NewString(), consumer.ID, "foreign", "ck_foreign", make([]byte, 32),
		[]string{"read"}, nil, quota); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("issue for another account's consumer: %v, want ErrNotFound", err)
	}
}

func TestMemConsumerKeyQuotaHoldsUnderConcurrentIssues(t *testing.T) {
	consumerKeyQuotaContract(t, state.NewMemStore())
}
