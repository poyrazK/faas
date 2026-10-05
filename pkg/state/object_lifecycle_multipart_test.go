package state_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 550
func TestObjectLifecycleMultipartMem(t *testing.T) {
	m := state.NewMemStore()
	var offset time.Duration
	m.SetClockForTest(func() time.Time { return time.Now().UTC().Add(offset) })
	objectLifecycleMultipartSuite(t, m, func(string) { offset += 4 * 24 * time.Hour }, func() state.ObjectLifecycleStore { return m })
}

// adr: 550
func TestObjectLifecycleMultipartPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	objectLifecycleMultipartSuite(t, s, func(id string) {
		if _, err := pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET created_at=created_at-interval '4 days' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}, func() state.ObjectLifecycleStore { return state.NewPgStore(pool) })
}

func objectLifecycleMultipartSuite(t *testing.T, st accountingStore, age func(string), reopen func() state.ObjectLifecycleStore) {
	t.Helper()
	ctx := t.Context()
	l := st.(state.ObjectLifecycleStore)
	sessions := st.(state.ObjectMultipartUploadStore)
	transfers := st.(state.ObjectMultipartTransferStore)
	b, u := activeTrackedUpload(t, st, "tmp/upload")
	if err := transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "part", 1, 30, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if err := transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "part"); err != nil {
		t.Fatal(err)
	}
	age(u.ID)
	u, err := sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	days := int32(1)
	rules := []api.ObjectLifecycleRule{
		{ID: "disabled", Status: "Disabled", AbortIncompleteMultipartDays: &days},
		{ID: "unmatched", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Prefix: "other/"}, AbortIncompleteMultipartDays: &days},
		{ID: "cleanup", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Prefix: "tmp/"}, AbortIncompleteMultipartDays: &days},
	}
	if _, err = l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, rules); err != nil {
		t.Fatal(err)
	}
	j, err := l.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || j.Phase != "multipart" {
		t.Fatal(j, err)
	}
	j, err = l.ClaimObjectLifecycleScan(ctx, j.ID, "scan")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.ListObjectLifecycleMultipartUploads(ctx, j.ID, "foreign"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("foreign discovery", err)
	}
	rows, err := l.ListObjectLifecycleMultipartUploads(ctx, j.ID, j.Token)
	if err != nil || len(rows) != 1 || rows[0].ID != u.ID {
		t.Fatal(rows, err)
	}
	if _, err = l.CheckpointObjectLifecycleMultipartUpload(ctx, j.ID, j.Token, state.ObjectMultipartUpload{}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("completed before admission", err)
	}
	for _, mutate := range []func(*state.ObjectMultipartUpload){
		func(v *state.ObjectMultipartUpload) { v.AccountID = uuid.NewString() },
		func(v *state.ObjectMultipartUpload) { v.AppID = uuid.NewString() },
		func(v *state.ObjectMultipartUpload) { v.BucketID = uuid.NewString() },
		func(v *state.ObjectMultipartUpload) { v.Key = "other/key" },
		func(v *state.ObjectMultipartUpload) { v.ProviderUploadID = "stale-native" },
		func(v *state.ObjectMultipartUpload) { v.CreatedAt = v.CreatedAt.Add(-time.Second) },
	} {
		bad := rows[0]
		mutate(&bad)
		if _, err = l.CheckpointObjectLifecycleMultipartUpload(ctx, j.ID, j.Token, bad); err == nil {
			t.Fatal("foreign/stale discovery admitted")
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := l.CheckpointObjectLifecycleMultipartUpload(ctx, j.ID, j.Token, rows[0])
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		} else if !errors.Is(e, state.ErrConflict) {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatal("duplicate admission", wins)
	}
	stored, err := sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil || stored.State != state.ObjectMultipartAborting || stored.LeaseToken != "" || stored.LifecycleAbort.ScanID != j.ID || stored.LifecycleAbort.RuleID != "cleanup" || stored.LifecycleAbort.ExpectedProviderUploadID != u.ProviderUploadID || !stored.LifecycleAbort.ExpectedCreatedAt.Equal(u.CreatedAt) {
		t.Fatal("admission identity", stored, err)
	}
	assertReserved := func(want int64) {
		t.Helper()
		usage, e := st.ObjectUsage(ctx, b.AccountID, time.Now())
		if e != nil || len(usage.Buckets) != 1 || usage.Buckets[0].MultipartBytes != want {
			t.Fatal("unverified reclamation", usage, e)
		}
	}
	assertReserved(30)
	j, err = reopen().GetObjectLifecycleScan(ctx, b.AccountID, b.ID, j.ID)
	if err != nil || j.LastUploadID != u.ID || j.ScannedUploads != 1 || j.Token != "" {
		t.Fatal("restart lost checkpoint", j, err)
	}
	if _, err = l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = reopen().ClaimObjectLifecycleScan(ctx, j.ID, "stale"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("cancelled rule admitted more work", err)
	}
	claimed, err := sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "recovery", state.ObjectMultipartAborting, nil, true)
	if err != nil || claimed.LifecycleAbort != stored.LifecycleAbort {
		t.Fatal("rule replacement lost cleanup receipt", claimed, err)
	}
	if err = transfers.FinishVerifiedObjectMultipartAbort(ctx, u.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	assertReserved(0)
}

// adr: 550
func TestObjectLifecycleMultipartBoundsPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	b, first := activeTrackedUpload(t, s, "old")
	days := int32(1)
	for i := range int(api.ObjectLifecycleMultipartPageSize) {
		u, err := s.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: fmt.Sprintf("old-%d", i), ExpiresAt: time.Now().Add(time.Hour)}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
			t.Fatal(err)
		}
		if err = s.ActivateObjectMultipartUpload(ctx, u.ID, "init", "native"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, []api.ObjectLifecycleRule{{ID: "abort", Status: "Enabled", AbortIncompleteMultipartDays: &days}}); err != nil {
		t.Fatal(err)
	}
	j, err := s.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = s.ClaimObjectLifecycleScan(ctx, j.ID, "scan")
	if err != nil {
		t.Fatal(err)
	}
	// A concurrently initiated upload sorts before every existing candidate.
	newID := "00000000-0000-4000-8000-000000000001"
	u, err := s.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: newID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "new", ExpiresAt: time.Now().Add(time.Hour)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = s.ActivateObjectMultipartUpload(ctx, u.ID, "init", "new-native"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListObjectLifecycleMultipartUploads(ctx, j.ID, j.Token)
	if err != nil || len(rows) != int(api.ObjectLifecycleMultipartPageSize) || rows[0].ID == newID {
		t.Fatal("unbounded discovery or post-cutoff upload", len(rows), err)
	}
	if _, err = s.CheckpointObjectLifecycleMultipartUpload(ctx, j.ID, j.Token, rows[1]); !errors.Is(err, state.ErrConflict) {
		t.Fatal("skipped undispatched candidate", err)
	}
	// Completion wins after discovery: advancing past it must not rewrite it
	// into an abort, even if its original creation time would now be eligible.
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET created_at=created_at-interval '4 days' WHERE id=$1`, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	first, err = s.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, first.ID, "complete", state.ObjectMultipartCompleting, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckpointObjectLifecycleMultipartUpload(ctx, j.ID, j.Token, first); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, first.ID)
	if err != nil || got.State != state.ObjectMultipartCompleting || got.LifecycleAbort.ScanID != "" {
		t.Fatal("completion overwritten", got, err)
	}
}

// adr: 550
func TestObjectLifecycleMultipartPhaseTransition(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprintf("pg=%t", pg), func(t *testing.T) {
			var st accountingStore = state.NewMemStore()
			if pg {
				st, _ = pgStore(t)
			}
			b, _ := seedAccounting(t, st)
			l := st.(state.ObjectLifecycleStore)
			ctx := t.Context()
			days := int32(1)
			p, err := l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, []api.ObjectLifecycleRule{{ID: "mixed", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: &days}, AbortIncompleteMultipartDays: &days}})
			if err != nil {
				t.Fatal(err)
			}
			j, err := l.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID)
			if err != nil || j.Phase != "objects" {
				t.Fatal(j, err)
			}
			j, err = l.ClaimObjectLifecycleScan(ctx, j.ID, "objects")
			if err != nil {
				t.Fatal(err)
			}
			j, err = l.CheckpointObjectLifecycleScan(ctx, j.ID, j.Token, "", true)
			if err != nil || j.Phase != "multipart" || j.State != "scanning" || j.FinishedAt != nil {
				t.Fatal("object discovery prematurely completed a mixed scan", j, err)
			}
			got, err := l.GetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID)
			if err != nil || !got.NextScanAt.Equal(p.NextScanAt) {
				t.Fatal("phase transition delayed multipart work", got, err)
			}
			j, err = l.ClaimObjectLifecycleScan(ctx, j.ID, "multipart")
			if err != nil {
				t.Fatal(err)
			}
			j, err = l.CheckpointObjectLifecycleMultipartUpload(ctx, j.ID, j.Token, state.ObjectMultipartUpload{})
			if err != nil || j.State != "completed" || j.FinishedAt == nil || j.ScannedUploads != 0 {
				t.Fatal("empty multipart discovery did not complete", j, err)
			}
		})
	}
}
