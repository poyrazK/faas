//go:build !no_pg

// adr:375
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneObjectWriteFenceOwnershipAndRecovery(t *testing.T) {
	s, _, _ := pgWithPool(t)
	cloneObjectWriteFenceContract(t, s)
}

func TestPgCloneObjectWriteFenceLeaseExpiryAfterSourceLock(t *testing.T) {
	for _, phase := range []string{"acquire", "read", "abandon"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx, pool := pgWithPool(t)
			l, buckets, _ := cloneObjectWriteFenceFixture(t, s)
			if phase != "acquire" {
				for _, b := range buckets {
					if _, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, b.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if phase == "abandon" {
				op := l.Operation
				var err error
				l.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, nil, "")
				if err != nil {
					t.Fatal(err)
				}
			}
			// SQL clock is authoritative; shorten only this test's leased row.
			if err := pool.QueryRow(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '500 milliseconds' where id=$1 returning lease_until", l.Operation.ID).Scan(&l.ExpiresAt); err != nil {
				t.Fatal(err)
			}
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := lock.Exec(ctx, "select id from object_buckets where id=any($1::uuid[]) for update", []string{buckets[0].ID, buckets[1].ID}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch phase {
				case "acquire":
					_, err = s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, buckets[0].ID)
				case "read":
					_, err = s.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, l)
				case "abandon":
					err = s.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, l)
				}
				done <- err
			}()
			waitCloneObjectWriteFenceLock(t, ctx, pool)
			time.Sleep(max(0, time.Until(l.ExpiresAt)) + 30*time.Millisecond)
			if err := lock.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired %s: %v", phase, err)
			}
			var count int
			want := 2
			if phase == "acquire" {
				want = 0
			}
			if err := pool.QueryRow(ctx, "select count(*) from object_bucket_write_fences where clone_operation_id=$1", l.Operation.ID).Scan(&count); err != nil || count != want {
				t.Fatalf("expired mutation committed: count=%d want=%d err=%v", count, want, err)
			}
		})
	}
}

func waitCloneObjectWriteFenceLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := pool.QueryRow(ctx, "select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like '%ObjectBucketMutationLock%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("clone fence worker never blocked on the source lock")
}

func TestPgCloneObjectWriteFenceRejectsPlacementSubstitution(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	l, buckets, _ := cloneObjectWriteFenceFixture(t, s)
	b := buckets[0]
	if _, err := pool.Exec(ctx, "update object_buckets set physical_name=$2 where id=$1", b.ID, b.PhysicalName+"-substituted"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, b.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("fenced substituted source: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from object_bucket_write_fences where clone_operation_id=$1", l.Operation.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("substitution wrote fence: count=%d err=%v", count, err)
	}
}
