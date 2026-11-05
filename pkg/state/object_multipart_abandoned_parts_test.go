package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectMultipartAbandonedParts pins the 11-04 prod stuck abort: a part
// the client abandoned mid-transfer stayed dispatched and unsettled, so the
// upload's abort answered conflict forever and its bucket could not be
// deleted. After the attempt's transfer window, a verified native abort
// settles it.
func TestObjectMultipartAbandonedPartsMem(t *testing.T) {
	st := state.NewMemStore()
	multipartAbandonedPartsSuite(t, st, func(string) {
		later := time.Now().Add(time.Hour)
		st.SetClockForTest(func() time.Time { return later })
	})
}

func TestObjectMultipartAbandonedPartsPG(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	multipartAbandonedPartsSuite(t, st, func(upload string) {
		// The grant guard keeps a dispatched attempt's window fixed; in
		// production it ends with time. Move it into the past on an isolated
		// connection with triggers disabled for this one fixture update.
		ctx := context.Background()
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		for _, sql := range []string{`set session_replication_role = replica`, `UPDATE object_storage_multipart_part_grants SET unsafe_until=clock_timestamp()-interval '1 second' WHERE upload_id=$1`, `set session_replication_role = origin`} {
			args := []any{}
			if strings.Contains(sql, "$1") {
				args = append(args, upload)
			}
			if _, err := conn.Exec(ctx, sql, args...); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func multipartAbandonedPartsSuite(t *testing.T, st accountingStore, expire func(string)) {
	ctx := t.Context()
	b, u := activeTrackedUpload(t, st, "abandoned")
	sessions := st.(state.ObjectMultipartUploadStore)
	transfers := st.(state.ObjectMultipartTransferStore)
	settler := st.(state.ObjectMultipartAbandonedPartSettler)
	if err := transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "attempt", 1, 3, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.(state.ObjectMultipartPartMutationStore).DispatchObjectMultipartPartMutation(ctx, b, u.ID, 1, "attempt"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false); err != nil {
		t.Fatal(err)
	}
	if n, err := settler.SettleAbandonedObjectMultipartPartWriters(ctx, u.ID, "abort"); err != nil || n != 0 {
		t.Fatal("settled an attempt inside its transfer window", n, err)
	}
	if ready, err := transfers.ObjectMultipartAbortReady(ctx, u.ID, "abort"); err != nil || ready {
		t.Fatal("abort ready with an attempt in flight", ready, err)
	}
	expire(u.ID)
	if _, err := settler.SettleAbandonedObjectMultipartPartWriters(ctx, u.ID, "other"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("settled under another lease", err)
	}
	if n, err := settler.SettleAbandonedObjectMultipartPartWriters(ctx, u.ID, "abort"); err != nil || n != 1 {
		t.Fatal("the abandoned attempt was not settled", n, err)
	}
	if ready, err := transfers.ObjectMultipartAbortReady(ctx, u.ID, "abort"); err != nil || !ready {
		t.Fatal("abort still waits for the settled attempt", ready, err)
	}
	if err := transfers.FinishVerifiedObjectMultipartAbort(ctx, u.ID, "abort"); err != nil {
		t.Fatal("abort could not finish", err)
	}
}
