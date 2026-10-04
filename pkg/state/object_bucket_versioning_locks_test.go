package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// adr: 421
// Account deletion and quota admissions take the account lock first. A waiting
// cutover must leave the bucket lock available to that existing owner.
func TestObjectVersioningAccountBeforeBucketPG(t *testing.T) {
	for _, observe := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "observation"}[observe], func(t *testing.T) {
			st, pool, _ := pgStoreWithPool(t)
			b, _ := seedAccounting(t, st)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background()) //nolint:errcheck
			var pid int32
			if err = tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", b.AccountID); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				var e error
				if observe {
					_, e = st.ObserveObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Enabled")
				} else {
					_, e = st.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Enabled")
				}
				result <- e
			}()
			waitErr := waitVersioningAccountLock(ctx, pool, pid)
			if waitErr == nil {
				_, waitErr = tx.Exec(ctx, "SELECT id FROM object_buckets WHERE id=$1 FOR NO KEY UPDATE", b.ID)
			}
			if err = tx.Rollback(context.Background()); waitErr != nil || err != nil {
				cancel()
				<-result
				t.Fatal("versioning held a bucket while waiting on account", waitErr, err)
			}
			if err = <-result; err != nil {
				t.Fatal(err)
			}
			j, err := st.GetObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID)
			if err != nil || j.DesiredStatus != "Enabled" || j.State != "waiting" {
				t.Fatal(j, err)
			}
		})
	}
}

func waitVersioningAccountLock(ctx context.Context, pool *pgxpool.Pool, pid int32) error {
	for {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE datname=current_database() AND wait_event_type='Lock'
 AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%FROM accounts%FOR UPDATE%')`, pid).Scan(&blocked)
		if err != nil || blocked {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
