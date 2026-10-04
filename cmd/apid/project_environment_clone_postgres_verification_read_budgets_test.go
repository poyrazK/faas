//go:build !no_pg

// adr:568
package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/state"
)

type verificationReadBudgetFailureStore struct {
	*verificationRetryFailureStore
	loseBudget  bool
	loseDebit   int32
	beforeDebit func(state.ProjectEnvironmentClonePostgresVerificationReadRequest)
}

func (s *verificationReadBudgetFailureStore) ReserveProjectEnvironmentClonePostgresVerificationReadBudget(ctx context.Context, l state.ProjectEnvironmentCloneLease, r state.ProjectEnvironmentClonePostgresVerificationReadBudgetRequest, limits state.ProjectEnvironmentClonePostgresVerificationReadBudgetLimits) (state.ProjectEnvironmentClonePostgresVerificationReadBudget, bool, error) {
	b, first, err := s.PgStore.ReserveProjectEnvironmentClonePostgresVerificationReadBudget(ctx, l, r, limits)
	if err == nil && s.loseBudget {
		s.loseBudget = false
		return b, false, managedpostgres.ErrUnavailable
	}
	return b, first, err
}
func (s *verificationReadBudgetFailureStore) AllocateProjectEnvironmentClonePostgresVerificationRead(ctx context.Context, l state.ProjectEnvironmentCloneLease, r state.ProjectEnvironmentClonePostgresVerificationReadRequest) (state.ProjectEnvironmentClonePostgresVerificationReadBudget, bool, error) {
	if s.beforeDebit != nil {
		s.beforeDebit(r)
	}
	b, first, err := s.PgStore.AllocateProjectEnvironmentClonePostgresVerificationRead(ctx, l, r)
	if err == nil && s.loseDebit == r.Attempt {
		s.loseDebit = 0
		return b, false, managedpostgres.ErrUnavailable
	}
	return b, first, err
}
func readBudgetWorkerFixture(t *testing.T) (*verificationWorkerFixture, *verificationReadBudgetFailureStore) {
	t.Helper()
	v, base := retryVerificationWorkerFixture(t)
	store := &verificationReadBudgetFailureStore{verificationRetryFailureStore: base}
	v.f.db.x.f.srv.store = store
	return v, store
}
func verificationReadBudget(t *testing.T, v *verificationWorkerFixture) state.ProjectEnvironmentClonePostgresVerificationReadBudget {
	t.Helper()
	f, x := v.f, v.f.db.x
	b, err := v.store.ProjectEnvironmentClonePostgresVerificationReadBudgetForLease(t.Context(), x.f.lease, x.source.source.ID, f.sourceOID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func assertVerificationReadNotOpened(t *testing.T, v *verificationWorkerFixture, attempt int32, owner string) {
	t.Helper()
	x := v.f.db.x
	var allow bool
	if err := x.targetRoot.QueryRow(t.Context(), `SELECT datallowconn FROM pg_database WHERE oid=$1::oid`, v.f.target.DatabaseOID).Scan(&allow); err != nil || allow {
		t.Fatal("read debit followed native opening", err)
	}
	table := "gregale_copy_database_verification.windows"
	if attempt > 1 {
		table = "gregale_copy_database_verification_retries.windows"
	}
	var exists bool
	if err := x.targetRoot.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		var count int
		query := `SELECT count(*) FROM gregale_copy_database_verification.windows WHERE owner_id=$1::uuid`
		if attempt > 1 {
			query = `SELECT count(*) FROM gregale_copy_database_verification_retries.windows WHERE owner_id=$1::uuid`
		}
		if err := x.targetRoot.QueryRow(t.Context(), query, owner).Scan(&count); err != nil || count != 0 {
			t.Fatal("native owner already existed before debit", err)
		}
	}
}

func TestPGClonePostgresVerificationReadBudgetWorkerLostResponsesKeepOriginalCaps(t *testing.T) {
	for _, phase := range []string{"budget", "first debit", "retry debit"} {
		t.Run(phase, func(t *testing.T) {
			v, store := readBudgetWorkerFixture(t)
			v.importData(t, false)
			if phase == "retry debit" {
				failFirstVerificationWorker(t, v)
				store.loseDebit = 2
			} else {
				store.loseBudget = phase == "budget"
				if phase == "first debit" {
					store.loseDebit = 1
				}
			}
			calls := 0
			store.beforeDebit = func(r state.ProjectEnvironmentClonePostgresVerificationReadRequest) {
				calls++
				assertVerificationReadNotOpened(t, v, r.Attempt, r.VerificationID)
			}
			reads := v.dataReads
			got, err := runVerificationRetryWorker(t, v)
			if !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) || v.dataReads != reads {
				t.Fatal("lost budget response opened comparison", err)
			}
			held := verificationReadBudget(t, v)
			expected := 0
			if phase == "first debit" {
				expected = 1
			}
			if phase == "retry debit" {
				expected = 2
			}
			if len(held.Allocations) != expected || held.ReadBytes != 3*v.cfg.MaxBytes {
				t.Fatal("committed admission not retained")
			}
			if phase != "budget" {
				head := v.owner(t)
				attempt := int32(1)
				if phase == "retry debit" {
					h := verificationRetryHistory(t, v)
					head = h[len(h)-1].ProjectEnvironmentClonePostgresVerification
					attempt = headAttempt(h)
				}
				assertVerificationReadNotOpened(t, v, attempt, head.VerificationID)
			}
			v.handoff(t)
			// Committed debits recover their caps even when the worker is configured
			// too narrowly to compare the real data. An undispatched held aggregate
			// cannot acquire larger sort caps on a later worker either.
			if phase != "budget" {
				v.cfg.MaxBytes = 1
				v.cfg.SortMemoryBytes = 32
				v.cfg.SortDiskBytes = 32
			}
			got, err = runVerificationRetryWorker(t, v)
			wantAttempt := int32(1)
			if phase == "retry debit" {
				wantAttempt = 2
			}
			if err != nil || got.State != "verified" || got.Attempt != wantAttempt || v.dataReads != reads+1 {
				t.Fatal("held caps or owner lost after handoff", err)
			}
			after := verificationReadBudget(t, v)
			if !held.CreatedAt.Equal(after.CreatedAt) || held.ReadBytes != after.ReadBytes || held.SortMemoryBytes != after.SortMemoryBytes || held.SortDiskBytes != after.SortDiskBytes {
				t.Fatal("aggregate reservation replaced")
			}
			if phase != "budget" && !reflect.DeepEqual(held, after) {
				t.Fatal("recovery replaced first debit")
			}
			if calls != 1 {
				t.Fatal("lost debit was allocated again", calls)
			}
			// Close-only matched replay accepts an absent reader configuration and
			// neither consumes new credits nor opens a new child read.
			spool := v.cfg.SpoolDir
			v.cfg = copycontents.Config{}
			replay, err := runVerificationRetryWorker(t, v)
			if err != nil || !reflect.DeepEqual(replay, got) || !reflect.DeepEqual(verificationReadBudget(t, v), after) || v.dataReads != reads+1 {
				t.Fatal("matched replay spent credits", err)
			}
			v.cfg.SpoolDir = spool
			v.assertClosed(t, true)
		})
	}
}
func headAttempt(h []state.ProjectEnvironmentClonePostgresVerificationAttempt) int32 {
	return h[len(h)-1].Attempt
}

func TestPGClonePostgresVerificationReadBudgetWorkerCannotResetAggregateOrSortCaps(t *testing.T) {
	for _, mode := range []string{"read", "sort"} {
		t.Run(mode, func(t *testing.T) {
			v, _ := readBudgetWorkerFixture(t)
			v.importData(t, false)
			old := v.cfg
			if mode == "read" {
				v.cfg.MaxBytes = 1
				if _, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrQuotaExceeded) {
					t.Fatal("initial read did not exhaust", err)
				}
			} else {
				failFirstVerificationWorker(t, v)
			}
			held := verificationReadBudget(t, v)
			original := verificationRetryHistory(t, v)[0]
			v.handoff(t)
			v.cfg = old
			if mode == "sort" {
				v.cfg.SortMemoryBytes = 128
				v.cfg.SortDiskBytes++
			}
			if got, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrQuotaExceeded) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) || v.dataReads != 1 {
				t.Fatal("changed worker exceeded original hold", err)
			}
			h := verificationRetryHistory(t, v)
			if len(h) != 2 || h[1].State != "verifying" || !reflect.DeepEqual(h[0], original) || !reflect.DeepEqual(verificationReadBudget(t, v), held) {
				t.Fatal("denied admission changed work credits/history")
			}
			assertVerificationReadNotOpened(t, v, 2, h[1].VerificationID)
			if mode == "sort" {
				v.cfg = old
				verified, err := runVerificationRetryWorker(t, v)
				if err != nil || verified.Attempt != 2 || v.dataReads != 2 {
					t.Fatal("unopened denied owner lost admission", err)
				}
			} else {
				v.cfg.MaxBytes = 1
				for attempt := 2; attempt <= 3; attempt++ {
					if _, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrQuotaExceeded) || v.dataReads != attempt {
						t.Fatal("bounded tiny attempt", attempt, err)
					}
				}
				b := verificationReadBudget(t, v)
				if b.AllocatedReadBytes != 3 || len(b.Allocations) != 3 || b.ReadBytes != 3 {
					t.Fatal("failed reads refunded")
				}
			}
			v.assertClosed(t, true)
		})
	}
}

func TestPGClonePostgresVerificationReadBudgetWorkerLegacyRecoveryCannotFundRead(t *testing.T) {
	for _, mode := range []string{"unmatched", "matched"} {
		t.Run(mode, func(t *testing.T) {
			v, _ := readBudgetWorkerFixture(t)
			v.importData(t, false)
			x := v.f.db.x
			if mode == "matched" {
				first, err := v.run(t)
				if err != nil {
					t.Fatal(err)
				}
				// Represent a pre-ledger verified owner with actual authenticated proof.
				if _, err = x.f.pool.Exec(t.Context(), `DELETE FROM project_environment_clone_postgres_verification_read_debits WHERE operation_id=$1`, x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = x.f.pool.Exec(t.Context(), `DELETE FROM project_environment_clone_postgres_verification_read_budgets WHERE operation_id=$1`, x.f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
				spool := v.cfg.SpoolDir
				v.cfg = copycontents.Config{}
				got, err := runVerificationRetryWorker(t, v)
				if err != nil || !reflect.DeepEqual(got.ProjectEnvironmentClonePostgresVerification, first) || v.dataReads != 1 {
					t.Fatal("legacy match not recovered close-only", err)
				}
				v.cfg.SpoolDir = spool
			} else {
				v.store.loseVerificationReserve = true
				if _, err := v.run(t); !errors.Is(err, managedpostgres.ErrUnavailable) {
					t.Fatal(err)
				}
				owner, _, err := v.store.ClaimProjectEnvironmentClonePostgresVerification(t.Context(), x.f.lease, x.source.source.ID, v.f.sourceOID)
				if err != nil {
					t.Fatal(err)
				}
				// Actual legacy native dispatch, with no child borrow or read callback.
				err = verificationAttemptBootstrap(t, v, func(ctx context.Context, conn *pgx.Conn, p clonePostgresDatabasePreparation) error {
					_, err := p.receipt.WithVerificationAccess(ctx, conn, v.f.db.exports, uuid.MustParse(owner.ImportID), uuid.MustParse(owner.VerificationID), func(context.Context, copyarchive.RestoreTarget) error { return nil }, func(context.Context, copydatabases.VerificationTarget) error { return managedpostgres.ErrUnavailable })
					return err
				})
				if !errors.Is(err, managedpostgres.ErrUnavailable) {
					t.Fatal("legacy native fixture", err)
				}
				for range 2 {
					if got, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrConflict) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) || v.dataReads != 0 {
						t.Fatal("legacy owner received new read", err)
					}
				}
				h := verificationRetryHistory(t, v)
				if len(h) != 1 || h[0].State != "failed" {
					t.Fatal("legacy closure not retained")
				}
			}
			var count int
			if err := x.f.pool.QueryRow(t.Context(), `SELECT count(*) FROM project_environment_clone_postgres_verification_read_budgets`).Scan(&count); err != nil || count != 0 {
				t.Fatal("legacy credits backfilled", err)
			}
			v.assertClosed(t, true)
		})
	}
}

// The ordinary Store interface does not expose optional private budget methods.
type verificationWithoutReadBudgetStore struct {
	state.Store
	state.ProjectEnvironmentClonePostgresVerificationStore
	state.ProjectEnvironmentClonePostgresVerificationAttemptStore
	state.ProjectEnvironmentClonePostgresContentsStore
	state.ProjectEnvironmentClonePostgresImportStore
	state.ProjectEnvironmentClonePostgresDatabaseSQLPinsStore
}

func TestPGClonePostgresVerificationReadBudgetWorkerRequiresStoreCapability(t *testing.T) {
	v, _ := readBudgetWorkerFixture(t)
	v.importData(t, false)
	x := v.f.db.x
	x.f.srv.store = &verificationWithoutReadBudgetStore{Store: v.store, ProjectEnvironmentClonePostgresVerificationStore: v.store, ProjectEnvironmentClonePostgresVerificationAttemptStore: v.store, ProjectEnvironmentClonePostgresContentsStore: v.store, ProjectEnvironmentClonePostgresImportStore: v.store, ProjectEnvironmentClonePostgresDatabaseSQLPinsStore: v.store}
	calls := x.p.targetSQLCalls
	if got, err := runVerificationRetryWorker(t, v); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) || v.dataReads != 0 || x.p.targetSQLCalls != calls {
		t.Fatal("missing private budget capability opened SQL", err)
	}
}

func TestPGClonePostgresVerificationReadBudgetWorkerRechecksDebitDuringActualRead(t *testing.T) {
	v, _ := readBudgetWorkerFixture(t)
	v.importData(t, false)
	x := v.f.db.x
	v.childTrace = &verificationWorkerReadTrace{onRead: func() {
		if _, err := x.f.pool.Exec(t.Context(), `UPDATE project_environment_clone_postgres_verification_read_budgets SET read_bytes=1 WHERE operation_id=$1`, x.f.lease.Operation.ID); err != nil {
			t.Fatal(err)
		}
	}}
	if got, err := runVerificationRetryWorker(t, v); err == nil || !reflect.DeepEqual(got, state.ProjectEnvironmentClonePostgresVerificationAttempt{}) || v.dataReads != 1 {
		t.Fatal("damaged debit published proof", err)
	}
	if owner := v.owner(t); owner.State != "verifying" || len(owner.Sealed.Ciphertext) != 0 {
		t.Fatal("invalid aggregate published match")
	}
	v.assertClosed(t, true)
}
