package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 560, 564
func TestObjectMultipartAdmissionLocksPG(t *testing.T) {
	for _, tc := range []struct {
		name, second string
		fixed        bool
	}{
		{"fixed_configuration", "SELECT id FROM object_buckets WHERE id=$1 FOR NO KEY UPDATE", true},
		{"fixed_tracked_write", "SELECT id FROM object_buckets WHERE id=$1 FOR SHARE", true},
		{"public_configuration", "SELECT id FROM object_buckets WHERE id=$1 FOR NO KEY UPDATE", false},
		{"public_tracked_write", "SELECT id FROM object_buckets WHERE id=$1 FOR SHARE", false},
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
			if _, err = fixture.Exec(ctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", b.AccountID); err != nil {
				t.Fatal(err)
			}
			candidate := fixedMultipartCandidate(b, tc.name)
			if !tc.fixed {
				candidate.SizeBytes, candidate.PartSizeBytes, candidate.PartCount = 0, 0, 0
			}
			result := make(chan error, 1)
			go func() {
				var e error
				if tc.fixed {
					_, e = st.ReserveAdmittedObjectMultipartUpload(ctx, candidate, 100, accountingPolicy())
				} else {
					_, e = st.ReserveObjectMultipartUpload(ctx, candidate, 100)
				}
				result <- e
			}()
			// Wait until the admission reaches this precise contested lock.
			err = waitVersioningAccountLock(ctx, pool, pid)
			if err == nil {
				_, err = fixture.Exec(ctx, tc.second, b.ID)
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
			if err != nil || got.FixedAdmission != tc.fixed || got.State != state.ObjectMultipartInitiating {
				t.Fatal(got, err)
			}
			if tc.fixed {
				fixedMultipartUsage(t, st, b, accountingPolicy(), 5, 1, 1)
			} else {
				fixedMultipartUsage(t, st, b, accountingPolicy(), 0, 0, 0)
			}
		})
	}
}
