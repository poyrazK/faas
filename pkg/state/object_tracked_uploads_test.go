package state_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type trackedUploadStore interface {
	accountingStore
	state.ObjectTrackedUploadStore
	state.ObjectUploadRouteStore
	state.ObjectCapacityStore
}

func TestTrackedObjectUploadsMem(t *testing.T) { trackedObjectUploadSuite(t, state.NewMemStore()) }
func TestTrackedObjectUploadsPG(t *testing.T)  { st, _ := pgStore(t); trackedObjectUploadSuite(t, st) }
func trackedObjectUploadSuite(t *testing.T, st trackedUploadStore) {
	ctx := context.Background()
	b, _ := seedAccounting(t, st)
	p := accountingPolicy()
	route, err := st.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, Name: "files", BucketID: b.ID, MaxBytes: 100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c := state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "subject", Key: "upload/key", Bytes: 10, ContentType: "image/png", Status: "pending", IdempotencyKey: "once", RequestFingerprint: "fingerprint"}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidate := c
			candidate.ID = uuid.NewString()
			_, created, e := st.BeginTrackedObjectUpload(ctx, candidate, p)
			if e != nil {
				t.Error(e)
			}
			if created {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("duplicate owners", winners.Load())
	}
	c, created, err := st.BeginTrackedObjectUpload(ctx, c, p)
	if err != nil || created || c.WritePhase != state.ObjectUploadPrepared {
		t.Fatal(c, created, err)
	}
	usage, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Authorizations != 1 || usage.Buckets[0].GrantedBytes != 10 || usage.Buckets[0].GrantedKeys != 1 {
		t.Fatal("duplicate quota", usage, err)
	}
	// Replay remains available with a spent budget or an active write fence.
	exhausted := p
	exhausted.MaxAccountBytes = 1
	if _, created, err = st.BeginTrackedObjectUpload(ctx, c, exhausted); err != nil || created {
		t.Fatal("replay readmitted", created, err)
	}
	foreign := c
	foreign.ID = uuid.NewString()
	foreign.IdempotencyKey = "foreign"
	foreign.AppID = uuid.NewString()
	if _, _, err = st.BeginTrackedObjectUpload(ctx, foreign, p); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign route", err)
	}
	// Route admissions cannot be settled by the unrelated proxy API.
	if err = st.SettleObjectWrite(ctx, b.AccountID, b.ID, c.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("proxy bypass", err)
	}
	j, err := st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err = st.BeginTrackedObjectUpload(ctx, c, p); err != nil || created {
		t.Fatal("fenced replay", err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "waiting")
	if err != nil || j.State != "waiting" || j.PendingWrites != 1 || j.LastErrorCode != "unsettled_writes" {
		t.Fatal("unsafe readiness", j, err)
	}
	if _, err = st.FinishObjectCapacityReconciliation(ctx, j.ID, "waiting", 0, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatal("pending refunded", err)
	}
	winners.Store(0)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID)
			if e == nil {
				winners.Add(1)
			} else if !errors.Is(e, state.ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("duplicate provider dispatch", winners.Load())
	}
	c.Status = "completed"
	c.ETag = "etag"
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.Status = "failed"
	c.ETag = ""
	c.ErrorCode = "provider_write_rejected"
	if _, err = st.FinishTrackedObjectUpload(ctx, c); !errors.Is(err, state.ErrConflict) {
		t.Fatal("terminal receipt rewritten", err)
	}
	legacyUpdate := c
	if _, err = st.UpdateObjectUploadCompletion(ctx, legacyUpdate); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("legacy update changed tracked receipt", err)
	}
	if _, err = st.CancelObjectCapacityReconciliation(ctx, b.AccountID, b.ID, j.ID); err != nil {
		t.Fatal(err)
	}
	j, err = st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "scan")
	if err != nil || j.State != "scanning" {
		t.Fatal("route grant remained conservative", j, err)
	}
	if _, err = st.FinishObjectCapacityReconciliation(ctx, j.ID, "scan", 0, 0); err != nil {
		t.Fatal(err)
	}
	c.Status = "pending"
	replay, created, err := st.BeginTrackedObjectUpload(ctx, c, p)
	if err != nil || created || replay.Status != "completed" || replay.ETag != "etag" {
		t.Fatal("receipt lost during rebase", replay, err)
	}
	fresh := c
	fresh.ID = uuid.NewString()
	fresh.Key = "upload/new"
	fresh.Bytes = 100
	fresh.IdempotencyKey = ""
	fresh.RequestFingerprint = ""
	fresh.Status = "pending"
	fresh.ETag = ""
	fresh.ErrorCode = ""
	if _, created, err = st.BeginTrackedObjectUpload(ctx, fresh, p); err != nil || !created {
		t.Fatal("capacity not reusable", created, err)
	}
	// A failed transaction must not leave an intent or spend an authorization.
	invalid := fresh
	invalid.ID = uuid.NewString()
	invalid.Key = "over-limit"
	invalid.IdempotencyKey = "rejected"
	invalid.RequestFingerprint = "fingerprint"
	if _, _, err = st.BeginTrackedObjectUpload(ctx, invalid, p); !errors.Is(err, state.ErrObjectCapacity) {
		t.Fatal(err)
	}
	if _, err = st.GetObjectUploadIntent(ctx, route.ID, c.SubjectID, "rejected"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed intent persisted", err)
	}
	usage, err = st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Authorizations != 2 || usage.Buckets[0].GrantedBytes != 100 {
		t.Fatal("admission not atomic", usage, err)
	}
}
