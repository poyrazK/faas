package state_test

import (
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 550
func TestObjectMultipartPartURLFenceMem(t *testing.T) {
	m := state.NewMemStore()
	objectMultipartPartURLFence(t, m, func(id string, deadline time.Time) {
		m.SetClockForTest(func() time.Time { return deadline.Add(time.Second) })
	}, func() multipartPartURLStore { return m })
}

// adr: 550
func TestObjectMultipartPartURLAdmissionRaceMem(t *testing.T) {
	objectMultipartPartURLAdmissionRace(t, state.NewMemStore())
}

// adr: 550
func TestObjectMultipartPartURLAdmissionRacePG(t *testing.T) {
	s, _, _ := pgStoreWithPool(t)
	objectMultipartPartURLAdmissionRace(t, s)
}

func objectMultipartPartURLAdmissionRace(t *testing.T, st accountingStore) {
	t.Helper()
	ctx := t.Context()
	s := st.(multipartPartURLStore)
	b, _ := seedAccounting(t, st)
	for _, operation := range []string{state.ObjectMultipartAborting, state.ObjectMultipartCompleting} {
		for i := range 8 {
			id := uuid.NewString()
			u, err := s.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: id, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: operation + strconv.Itoa(i), SizeBytes: 1, PartSizeBytes: 1, PartCount: 1, ExpiresAt: time.Now().Add(time.Hour)}, 20)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, id, "start", state.ObjectMultipartInitiating, nil, false); err != nil {
				t.Fatal(err)
			}
			if err = s.ActivateObjectMultipartUpload(ctx, id, "start", "native-"+id); err != nil {
				t.Fatal(err)
			}
			u, err = s.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, id)
			if err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			signed, claimed := make(chan error, 1), make(chan error, 1)
			expires := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
			go func() { <-start; signed <- s.RecordObjectMultipartPartURL(ctx, u, expires) }()
			go func() {
				<-start
				_, e := s.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, id, "mutation", operation, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}, false)
				claimed <- e
			}()
			close(start)
			signErr, claimErr := <-signed, <-claimed
			if claimErr != nil || signErr != nil && !errors.Is(signErr, state.ErrConflict) {
				t.Fatal("race failed", signErr, claimErr)
			}
			stored, err := s.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, id)
			if err != nil || stored.State != operation {
				t.Fatal("mutation lost its cutoff", stored, err)
			}
			if signErr == nil && !stored.PartURLUnsafeUntil.Equal(expires.Add(api.ObjectTransferTimeout+api.ObjectMultipartCleanupGrace)) {
				t.Fatal("published URL lost its fence", stored)
			}
			if errors.Is(signErr, state.ErrConflict) && !stored.PartURLUnsafeUntil.IsZero() {
				t.Fatal("rejected URL changed its fence", stored)
			}
			if err = s.RecordObjectMultipartPartURL(ctx, u, expires); !errors.Is(err, state.ErrConflict) {
				t.Fatal("post-cutoff signing", err)
			}
			if operation == state.ObjectMultipartAborting && signErr == nil {
				if ready, e := s.ObjectMultipartAbortReady(ctx, id, "mutation"); e != nil || ready {
					t.Fatal("race refunded a published URL", ready, e)
				}
			}
		}
	}
}

// adr: 550
func TestObjectMultipartPartURLFencePG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	objectMultipartPartURLFence(t, s, func(id string, deadline time.Time) {
		// Move this isolated fixture past its deadline. Migration acceptance
		// separately verifies that production callers cannot shorten it.
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err = tx.Exec(ctx, `ALTER TABLE object_storage_multipart_uploads DISABLE TRIGGER object_multipart_part_url_deadline_protected`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE object_storage_multipart_uploads SET created_at=created_at-interval '1 hour',part_url_unsafe_until=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `ALTER TABLE object_storage_multipart_uploads ENABLE TRIGGER object_multipart_part_url_deadline_protected`); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}, func() multipartPartURLStore { return state.NewPgStore(pool) })
}

type multipartPartURLStore interface {
	state.ObjectMultipartUploadStore
	state.ObjectMultipartPartURLStore
	state.ObjectMultipartTransferStore
}

func objectMultipartPartURLFence(t *testing.T, st accountingStore, expire func(string, time.Time), reopen func() multipartPartURLStore) {
	t.Helper()
	ctx := t.Context()
	s := st.(multipartPartURLStore)
	b, _ := seedAccounting(t, st)
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "legacy", 10, true, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	u, err := s.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "legacy", SizeBytes: 10, PartSizeBytes: 10, PartCount: 1, ExpiresAt: time.Now().Add(time.Hour)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "start", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = s.ActivateObjectMultipartUpload(ctx, u.ID, "start", "private-upload"); err != nil {
		t.Fatal(err)
	}
	u, err = s.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	for _, foreign := range []state.ObjectMultipartUpload{
		func() state.ObjectMultipartUpload { v := u; v.AccountID = uuid.NewString(); return v }(),
		func() state.ObjectMultipartUpload { v := u; v.AppID = uuid.NewString(); return v }(),
		func() state.ObjectMultipartUpload { v := u; v.BucketID = uuid.NewString(); return v }(),
	} {
		if err = s.RecordObjectMultipartPartURL(ctx, foreign, expires); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign URL published", err)
		}
	}
	for _, invalid := range []time.Time{{}, time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
		if err = s.RecordObjectMultipartPartURL(ctx, u, invalid); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid URL deadline", invalid, err)
		}
	}
	for _, stale := range []state.ObjectMultipartUpload{
		func() state.ObjectMultipartUpload { v := u; v.Key = "another-key"; return v }(),
		func() state.ObjectMultipartUpload { v := u; v.ProviderUploadID = "another-native-upload"; return v }(),
	} {
		if err = s.RecordObjectMultipartPartURL(ctx, stale, expires); !errors.Is(err, state.ErrConflict) {
			t.Fatal("stale provider identity published", err)
		}
	}
	if err = s.RecordObjectMultipartPartURL(ctx, u, expires); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectMultipartPartURL(ctx, u, expires.Add(-time.Second)); err != nil {
		t.Fatal("shorter replay", err)
	}
	stored, err := reopen().GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	deadline := expires.Add(api.ObjectTransferTimeout + api.ObjectMultipartCleanupGrace)
	if err != nil || !stored.PartURLUnsafeUntil.Equal(deadline) {
		t.Fatal("URL fence shortened or lost on restart", stored, err)
	}
	if after, err := st.ObjectUsage(ctx, b.AccountID, time.Now()); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("URL fence changed admission accounting", before, after, err)
	}
	if _, err = s.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectMultipartPartURL(ctx, u, expires); !errors.Is(err, state.ErrConflict) {
		t.Fatal("URL published after abort cutoff", err)
	}
	if ready, err := reopen().ObjectMultipartAbortReady(ctx, u.ID, "abort"); err != nil || ready {
		t.Fatal("live URL considered drained", ready, err)
	}
	if err = s.FinishObjectMultipartUpload(ctx, u.ID, "abort", state.ObjectMultipartAborted); !errors.Is(err, state.ErrConflict) {
		t.Fatal("generic abort bypassed proof", err)
	}
	if err = reopen().FinishVerifiedObjectMultipartAbort(ctx, u.ID, "abort"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("live URL refunded", err)
	}
	if after, err := st.ObjectUsage(ctx, b.AccountID, time.Now()); err != nil || after.Buckets[0].GrantedBytes != 10 {
		t.Fatal("aborting session lost its reservation", after, err)
	}
	if _, err = st.(state.ObjectCapacityStore).RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("live URL allowed capacity reconciliation", err)
	}
	expire(u.ID, deadline)
	if ready, err := reopen().ObjectMultipartAbortReady(ctx, u.ID, "abort"); err != nil || !ready {
		t.Fatal("elapsed URL remained fenced", ready, err)
	}
	if err = reopen().FinishVerifiedObjectMultipartAbort(ctx, u.ID, "abort"); err != nil {
		t.Fatal(err)
	}
	if after, err := st.ObjectUsage(ctx, b.AccountID, time.Now()); err != nil || after.Buckets[0].GrantedBytes != 10 || after.Buckets[0].MultipartBytes != 0 || after.Buckets[0].BaselineBytes != before.Buckets[0].BaselineBytes {
		t.Fatal("verified abort accounting", after, err)
	}
	if _, err = st.(state.ObjectCapacityStore).RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID); err != nil {
		t.Fatal("verified abort retained the live-session fence", err)
	}
}
