package state_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAccountDeletionGuardError(t *testing.T) {
	plain := errors.New("connection reset")
	unrelated := &pgconn.PgError{Code: "23514", ConstraintName: "instances_state_check"}
	databases := func(detail string) error {
		return fmt.Errorf("schedule: %w", &pgconn.PgError{Code: "23514", ConstraintName: "account_has_managed_postgres_databases", Detail: detail})
	}
	tests := []struct {
		name string
		in   error
		live int64 // -1: not the managed postgres guard
		is   error
	}{
		{name: "nil", in: nil, live: -1},
		{name: "other error", in: plain, live: -1, is: plain},
		{name: "shared app ownership", in: &pgconn.PgError{Code: "23514", ConstraintName: "account_shared_app_ownership_pending"}, live: -1, is: state.ErrOrgAppOwnershipPending},
		{name: "databases with count", in: databases("3"), live: 3, is: state.ErrAccountManagedPostgresDatabases},
		{name: "databases from an older guard", in: databases(""), live: 0, is: state.ErrAccountManagedPostgresDatabases},
		{name: "databases with unreadable count", in: databases("many"), live: 0, is: state.ErrAccountManagedPostgresDatabases},
		{name: "unrelated check", in: unrelated, live: -1, is: unrelated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := state.AccountDeletionGuardError(tt.in)
			if tt.in == nil && got != nil {
				t.Fatalf("nil became %v", got)
			}
			if tt.is != nil && !errors.Is(got, tt.is) {
				t.Fatalf("got %v, want %v", got, tt.is)
			}
			var blocked *state.AccountManagedPostgresDatabasesError
			if found := errors.As(got, &blocked); found != (tt.live >= 0) || found && blocked.Live != tt.live {
				t.Fatalf("got %#v, want live %d", got, tt.live)
			}
		})
	}
}

// The deletion guard refuses every path into deleted_pending while a live
// personal database remains, and reports how many there are.
func TestPgStoreAccountDeletionBlockedByManagedPostgres(t *testing.T) {
	transitions := []struct {
		name string
		from state.AccountStatus
		move func(state.Store, string) error
	}{
		{name: "self-service", from: state.AccountActive, move: func(s state.Store, id string) error {
			return s.MarkAccountDeletionPending(t.Context(), id)
		}},
		{name: "dunning", from: state.AccountSuspended, move: func(s state.Store, id string) error {
			return s.MarkDunningStep(t.Context(), id, state.AccountSuspended, state.AccountDeletedPending)
		}},
		{name: "status", from: state.AccountActive, move: func(s state.Store, id string) error {
			return s.UpdateAccountStatus(t.Context(), id, state.AccountDeletedPending)
		}},
	}
	databases := []struct {
		name          string
		live, deleted int
	}{
		{name: "two live", live: 2},
		{name: "one live beside a deleted one", live: 1, deleted: 1},
		{name: "only deleted", deleted: 1},
	}
	st, pool, ctx := pgStoreWithPool(t) // one schema; every case owns its account
	for _, tr := range transitions {
		for _, dbs := range databases {
			t.Run(tr.name+"/"+dbs.name, func(t *testing.T) {
				acct, err := st.CreateAccount(ctx, strings.ReplaceAll(uuid.NewString(), "-", "")+"@example.com", api.PlanHobby)
				if err != nil {
					t.Fatal(err)
				}
				if tr.from != state.AccountActive {
					if err := st.UpdateAccountStatus(ctx, acct.ID, tr.from); err != nil {
						t.Fatal(err)
					}
				}
				for i := range dbs.live + dbs.deleted {
					dbState := "provisioning"
					if i >= dbs.live {
						dbState = "deleted"
					}
					if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint,state,deleted_at)
VALUES($1,$2,$3,'eu',17,'development','single_zone','test',$4,$5::text,CASE WHEN $5::text='deleted' THEN now() END)`,
						uuid.NewString(), acct.ID, fmt.Sprintf("db-%d", i), strings.Repeat("a", 64), dbState); err != nil {
						t.Fatal(err)
					}
				}
				err = tr.move(st, acct.ID)
				got, readErr := st.AccountByID(ctx, acct.ID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if dbs.live == 0 {
					if err != nil || got.Status != state.AccountDeletedPending {
						t.Fatalf("deleted databases blocked the deletion: %v (status %s)", err, got.Status)
					}
					return
				}
				var blocked *state.AccountManagedPostgresDatabasesError
				if !errors.As(err, &blocked) || !errors.Is(err, state.ErrAccountManagedPostgresDatabases) || blocked.Live != int64(dbs.live) {
					t.Fatalf("got %v, want %d live managed postgres databases", err, dbs.live)
				}
				if got.Status != tr.from {
					t.Fatalf("status changed to %s despite the guard", got.Status)
				}
			})
		}
	}
}
