package orgbilling_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/orgbilling"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type emailUpdater func(context.Context, state.Account) error

func (f emailUpdater) UpdateCustomerEmail(ctx context.Context, account state.Account) error {
	return f(ctx, account)
}

func emailStores(t *testing.T, test func(*testing.T, state.Store, state.Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { store := state.NewMemStore(); test(t, store, store) })
	t.Run("postgres", func(t *testing.T) {
		pool := pgtest.OpenMigrated(t)
		test(t, state.NewPgStore(pool), state.NewPgStore(pool))
	})
}

func emailOrg(t *testing.T, store state.Store) state.Org {
	t.Helper()
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "email-" + uuid.NewString()[:8], Name: "Email", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: org.ID, Provider: "polar", CustomerID: "customer-" + org.ID}); err != nil {
		t.Fatal(err)
	}
	return org
}

func savedEmail(t *testing.T, store state.Store, orgID string) string {
	t.Helper()
	account, err := state.BillingResourceAccount(t.Context(), store, orgID)
	if err != nil {
		t.Fatal(err)
	}
	return account.Email
}

func TestEmailChangesSerializeProviderAndLocalWrites(t *testing.T) {
	emailStores(t, func(t *testing.T, store state.Store, peer state.Store) {
		org := emailOrg(t, store)
		firstEntered, secondEntered, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		remote := ""
		var remoteMu sync.Mutex
		updater := emailUpdater(func(ctx context.Context, target state.Account) error {
			if target.ProviderCustomerID != "customer-"+org.ID {
				return errors.New("unqualified customer passed to provider")
			}
			remoteMu.Lock()
			remote = target.Email
			remoteMu.Unlock()
			if target.Email == "first@example.com" {
				close(firstEntered)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			} else {
				close(secondEntered)
			}
			return nil
		})
		first, second := make(chan error, 1), make(chan error, 1)
		go func() { first <- orgbilling.ChangeEmail(ctx, store, org.ID, "polar", "first@example.com", updater) }()
		select {
		case <-firstEntered:
		case <-ctx.Done():
			t.Fatal("first provider update did not start", ctx.Err())
		}
		go func() { second <- orgbilling.ChangeEmail(ctx, peer, org.ID, "polar", "second@example.com", updater) }()
		select {
		case <-secondEntered:
			t.Fatal("second provider update overtook the first local save")
		case <-time.After(100 * time.Millisecond):
		}
		unblock()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if err := <-second; err != nil {
			t.Fatal(err)
		}
		if local := savedEmail(t, store, org.ID); local != "second@example.com" || remote != local {
			t.Fatalf("local=%q provider=%q", local, remote)
		}
	})
}

func TestEmailProviderRefusalLeavesLocalEmailAndAllowsRetry(t *testing.T) {
	emailStores(t, func(t *testing.T, store state.Store, _ state.Store) {
		org := emailOrg(t, store)
		if err := orgbilling.ChangeEmail(t.Context(), store, org.ID, "polar", "original@example.com", nil); err != nil {
			t.Fatal(err)
		}
		for _, refusal := range []error{billing.ErrCustomerEmailInUse, errors.New("provider unavailable")} {
			updater := emailUpdater(func(context.Context, state.Account) error { return refusal })
			err := orgbilling.ChangeEmail(t.Context(), store, org.ID, "polar", "changed@example.com", updater)
			var upstream *orgbilling.CustomerEmailSyncError
			if !errors.Is(err, refusal) || !errors.As(err, &upstream) || savedEmail(t, store, org.ID) != "original@example.com" {
				t.Fatalf("refusal changed local state or lost its type: %v", err)
			}
		}
		if err := orgbilling.ChangeEmail(t.Context(), store, org.ID, "polar", "changed@example.com", nil); err != nil || savedEmail(t, store, org.ID) != "changed@example.com" {
			t.Fatal("retry after refusal", err)
		}
	})
}

func TestEmailMutationUsesOneDatabaseConnection(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	org := emailOrg(t, store)
	config := pool.Config()
	config.MaxConns = 1
	one, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(one.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := orgbilling.ChangeEmail(ctx, state.NewPgStore(one), org.ID, "polar", "single@example.com", emailUpdater(func(context.Context, state.Account) error { return nil })); err != nil {
		t.Fatal(err)
	}
}

func TestEmailMutationDoesNotUseAnotherProvidersCustomer(t *testing.T) {
	emailStores(t, func(t *testing.T, store state.Store, _ state.Store) {
		org := emailOrg(t, store)
		calls := 0
		updater := emailUpdater(func(context.Context, state.Account) error { calls++; return nil })
		if err := orgbilling.ChangeEmail(t.Context(), store, org.ID, "stripe", "local@example.com", updater); err != nil {
			t.Fatal(err)
		}
		if calls != 0 || savedEmail(t, store, org.ID) != "local@example.com" {
			t.Fatal("missing active binding used another provider's customer")
		}
	})
}
