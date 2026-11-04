package state_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func legacyResetStore(t *testing.T, postgres bool) (state.Store, *pgxpool.Pool) {
	t.Helper()
	if !postgres {
		return state.NewMemStore(), nil
	}
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool), pool
}

func TestLegacyPasswordResetIsSingleUseAndAtomic(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		t.Run(fmt.Sprintf("postgres=%v", postgres), func(t *testing.T) {
			st, _ := legacyResetStore(t, postgres)
			ctx := t.Context()
			acct, err := st.CreateAccount(ctx, "atomic-reset@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if err = st.SetAccountPassword(ctx, acct.ID, "old-password-hash"); err != nil {
				t.Fatal(err)
			}
			hash := api.HashToken([]byte("local-reset-token"))
			if err = st.IssueLoginToken(ctx, hash, acct.ID, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			reset := st.(state.LegacyPasswordResetStore)
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err = reset.ResetLegacyPassword(canceled, hash, "canceled-hash"); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled reset: %v", err)
			}
			var wg sync.WaitGroup
			results := make(chan string, 8)
			failures := make(chan error, 8)
			for i := range 8 {
				wg.Go(func() {
					phc := fmt.Sprintf("new-password-hash-%d", i)
					id, err := reset.ResetLegacyPassword(ctx, hash, phc)
					if err == nil && id == acct.ID {
						results <- phc
					} else {
						failures <- err
					}
				})
			}
			wg.Wait()
			close(results)
			close(failures)
			if len(results) != 1 || len(failures) != 7 {
				t.Fatalf("winners=%d failures=%d", len(results), len(failures))
			}
			for err := range failures {
				if !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("losing reset: %v", err)
				}
			}
			winning := <-results
			stored, err := st.AccountPasswordByAccountID(ctx, acct.ID)
			if err != nil || stored != winning {
				t.Fatalf("committed hash does not match winning token use: %v", err)
			}
			if _, err = reset.ResetLegacyPassword(ctx, hash, "replayed-hash"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("replay: %v", err)
			}
		})
	}
}

func TestLegacyPasswordResetWriteFailurePreservesLinkPostgres(t *testing.T) {
	st, pool := legacyResetStore(t, true)
	ctx := t.Context()
	acct, err := st.CreateAccount(ctx, "retry-reset@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetAccountPassword(ctx, acct.ID, "old-password-hash"); err != nil {
		t.Fatal(err)
	}
	hash := api.HashToken([]byte("retry-reset-token"))
	if err = st.IssueLoginToken(ctx, hash, acct.ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `alter table account_passwords add constraint injected_reset_failure check (hash <> 'new-password-hash')`); err != nil {
		t.Fatal(err)
	}
	reset := st.(state.LegacyPasswordResetStore)
	if _, err = reset.ResetLegacyPassword(ctx, hash, "new-password-hash"); err == nil {
		t.Fatal("expected password write failure")
	}
	stored, err := st.AccountPasswordByAccountID(ctx, acct.ID)
	if err != nil || stored != "old-password-hash" {
		t.Fatal("failed reset changed the password")
	}
	var unused bool
	if err = pool.QueryRow(ctx, `select consumed_at is null from login_tokens where account_id=$1`, acct.ID).Scan(&unused); err != nil || !unused {
		t.Fatalf("failed reset burned link: %v", err)
	}
	if _, err = pool.Exec(ctx, `alter table account_passwords drop constraint injected_reset_failure`); err != nil {
		t.Fatal(err)
	}
	if _, err = reset.ResetLegacyPassword(ctx, hash, "new-password-hash"); err != nil {
		t.Fatalf("retry with same link: %v", err)
	}
}

func TestLegacyPasswordResetExpiredLinkDoesNotChangePassword(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		t.Run(fmt.Sprintf("postgres=%v", postgres), func(t *testing.T) {
			st, _ := legacyResetStore(t, postgres)
			ctx := t.Context()
			acct, err := st.CreateAccount(ctx, "expired-reset@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if err = st.SetAccountPassword(ctx, acct.ID, "old-password-hash"); err != nil {
				t.Fatal(err)
			}
			hash := api.HashToken([]byte("expired-reset-token"))
			if err = st.IssueLoginToken(ctx, hash, acct.ID, time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if _, err = st.(state.LegacyPasswordResetStore).ResetLegacyPassword(ctx, hash, "new-password-hash"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("expired reset: %v", err)
			}
			stored, err := st.AccountPasswordByAccountID(ctx, acct.ID)
			if err != nil || stored != "old-password-hash" {
				t.Fatal("expired reset changed password")
			}
		})
	}
}
