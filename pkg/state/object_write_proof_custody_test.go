package state_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectWriteProofCustodyMem(t *testing.T) {
	objectWriteProofCustodySuite(t, state.NewMemStore())
}
func TestObjectWriteProofCustodyPG(t *testing.T) {
	s, _ := pgStore(t)
	objectWriteProofCustodySuite(t, s)
}

func objectWriteProofCustodySuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	writes := st.(state.ObjectCapacityStore)
	for range 10 {
		b, _ := seedAccounting(t, st)
		start := make(chan struct{})
		var wg sync.WaitGroup
		ids := []string{uuid.NewString(), uuid.NewString()}
		errs := make([]error, 2)
		for i := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[i] = writes.BeginObjectWrite(ctx, b.AccountID, b.ID, ids[i], "proof", 10, accountingPolicy())
			}()
		}
		close(start)
		wg.Wait()
		winner := 0
		if errs[0] != nil {
			winner = 1
		}
		if errs[winner] != nil || !errors.Is(errs[1-winner], state.ErrConflict) {
			t.Fatal("same-key writes crossed proof fence", errs)
		}
		if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "proof", 10, true, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
			t.Fatal("legacy writer replaced pending proof", err)
		}
		if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "proof", 0, false, accountingPolicy()); err != nil {
			t.Fatal("read blocked", err)
		}
		if err := writes.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "independent", 10, accountingPolicy()); err != nil {
			t.Fatal("other key blocked", err)
		}
		if err := writes.SettleObjectWrite(ctx, b.AccountID, b.ID, ids[winner]); err != nil {
			t.Fatal(err)
		}
		if err := writes.BeginObjectWrite(ctx, b.AccountID, b.ID, ids[1-winner], "proof", 10, accountingPolicy()); err != nil {
			t.Fatal("settled proof did not release key", err)
		}
	}

	// An unknown-size multipart session may upload independent parts, but its
	// completion cannot replace another pending PUT's receipt.
	b, _ := seedAccounting(t, st)
	sessions := st.(state.ObjectMultipartUploadStore)
	u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "proof", ExpiresAt: time.Now().Add(time.Hour)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "native"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err = writes.BeginObjectWrite(ctx, b.AccountID, b.ID, id, "proof", 10, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	capacity := st.(state.ObjectMultipartCapacityStore)
	if err = capacity.AdmitObjectMultipartCompletion(ctx, b.AccountID, b.ID, u.ID, u.Key, 10, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("multipart completion replaced proof", err)
	}
	if err = writes.SettleObjectWrite(ctx, b.AccountID, b.ID, id); err != nil {
		t.Fatal(err)
	}
	if err = capacity.AdmitObjectMultipartCompletion(ctx, b.AccountID, b.ID, u.ID, u.Key, 10, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
}

func TestObjectWriteProofCustodyDatabaseGuard(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, s)
	id := uuid.NewString()
	if err := s.BeginObjectWrite(ctx, b.AccountID, b.ID, id, "proof", 10, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO object_storage_write_admissions(id,bucket_id,key_hash,kind) SELECT $1,bucket_id,key_hash,'proxy' FROM object_storage_write_admissions WHERE id=$2`, []any{uuid.NewString(), id}},
		{`INSERT INTO object_storage_key_grants(bucket_id,key_hash,max_bytes) SELECT bucket_id,key_hash,10 FROM object_storage_write_admissions WHERE id=$1 ON CONFLICT(bucket_id,key_hash) DO UPDATE SET max_bytes=10`, []any{id}},
		{`UPDATE object_storage_key_grants SET max_bytes=max_bytes+1 WHERE bucket_id=$1`, []any{b.ID}},
	} {
		_, err := pool.Exec(ctx, tc.query, tc.args...)
		var check *pgconn.PgError
		if !errors.As(err, &check) || check.ConstraintName != "object_write_key_fenced" {
			t.Fatal("old writer bypassed custody", err)
		}
	}
}
