package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 550
func TestObjectMultipartResultsAccountBeforeBucketPG(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	for _, operation := range []string{"dispatch", "finish", "retry", "reject"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			b, u := preparedMultipartResult(t, st, operation, api.ObjectWriteConditions{IfNoneMatch: "*"})
			if operation != "dispatch" {
				if err := st.DispatchObjectMultipartCompletion(ctx, u); err != nil {
					t.Fatal(err)
				}
			}
			fixture, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fixture.Rollback(context.Background()) //nolint:errcheck
			var fixturePID int32
			if err = fixture.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&fixturePID); err != nil {
				t.Fatal(err)
			}
			// Configuration owns the account before requesting the bucket lock.
			if _, err = fixture.Exec(ctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", b.AccountID); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				var e error
				switch operation {
				case "dispatch":
					e = st.DispatchObjectMultipartCompletion(ctx, u)
				case "finish":
					_, e = st.FinishObjectMultipartCompletion(ctx, u, state.ObjectMultipartCompletionResult{ETag: `"actual"`})
				case "retry":
					e = st.RetryObjectMultipartCompletion(ctx, u, state.ObjectMultipartCompletionResult{RecoveryCursor: "cursor"}, "temporary", time.Second)
				case "reject":
					e = st.RejectObjectMultipartCompletionResult(ctx, u, state.ObjectMultipartCompletionResult{}, "precondition_failed")
				}
				result <- e
			}()
			// An account waiter must not already hold a conflicting bucket lock.
			err = waitVersioningAccountLock(ctx, pool, fixturePID)
			if err == nil {
				_, err = fixture.Exec(ctx, "SELECT id FROM object_buckets WHERE id=$1 FOR NO KEY UPDATE", b.ID)
			}
			if err == nil {
				err = fixture.Commit(ctx)
			} else {
				_ = fixture.Rollback(context.Background())
			}
			resultErr := <-result
			if err != nil || resultErr != nil {
				t.Fatalf("account/bucket lock ordering: fixture=%v result=%v", err, resultErr)
			}
			got, err := st.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			valid := got.CompletionDispatched
			switch operation {
			case "finish":
				valid = valid && got.State == state.ObjectMultipartCompleted && got.CompletionETag == `"actual"`
			case "retry":
				valid = valid && got.LeaseToken == "" && got.CompletionRecoveryCursor == "cursor"
			case "reject":
				valid = valid && got.State == state.ObjectMultipartAborting && got.CompletionErrorCode == "precondition_failed"
			}
			if !valid {
				t.Fatalf("%s lost its durable result: %+v", operation, got)
			}
		})
	}
}
