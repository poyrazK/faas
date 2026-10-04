package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 418
func TestObjectFixedMultipartAdmissionLocksPG(t *testing.T) {
	for _, tc := range []struct {
		name, first, waiter, second string
	}{
		{"configuration", "SELECT id FROM object_buckets WHERE id=$1 FOR NO KEY UPDATE", "%FROM object_buckets%FOR NO KEY UPDATE%", "SELECT id FROM accounts WHERE id=$1 FOR UPDATE"},
		{"tracked_write", "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", "%FROM accounts%FOR UPDATE%", "SELECT id FROM object_buckets WHERE id=$1 FOR KEY SHARE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, pool, _ := pgStoreWithPool(t)
			b, _ := seedAccounting(t, st)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			fixture, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fixture.Rollback(context.Background()) //nolint:errcheck
			var pid int32
			if err = fixture.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			first, second := b.ID, b.AccountID
			if tc.name == "tracked_write" {
				first, second = second, first
			}
			if _, err = fixture.Exec(ctx, tc.first, first); err != nil {
				t.Fatal(err)
			}
			candidate := fixedMultipartCandidate(b, tc.name)
			result := make(chan error, 1)
			go func() {
				_, e := st.ReserveAdmittedObjectMultipartUpload(ctx, candidate, 100, accountingPolicy())
				result <- e
			}()
			// Wait until the admission reaches this precise contested lock.
			for {
				var blocked bool
				err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE datname=current_database() AND wait_event_type='Lock'
 AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE $2)`, pid, tc.waiter).Scan(&blocked)
				if err != nil || blocked {
					break
				}
				select {
				case e := <-result:
					t.Fatalf("admission returned before contested lock: %v", e)
				case <-ctx.Done():
					err = ctx.Err()
				case <-time.After(10 * time.Millisecond):
				}
				if err != nil {
					break
				}
			}
			if err == nil {
				_, err = fixture.Exec(ctx, tc.second, second)
			}
			if err == nil {
				err = fixture.Commit(ctx)
			} else {
				_ = fixture.Rollback(context.Background())
			}
			if admissionErr := <-result; err != nil || admissionErr != nil {
				t.Fatal("multipart/bucket/account lock ordering", err, admissionErr)
			}
			got, err := st.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, candidate.ID)
			if err != nil || !got.FixedAdmission || got.State != state.ObjectMultipartInitiating {
				t.Fatal(got, err)
			}
			fixedMultipartUsage(t, st, b, accountingPolicy(), 5, 1, 1)
		})
	}
}
