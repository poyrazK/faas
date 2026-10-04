package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// adr: 535
func TestGatewayUploadReceiptsMem(t *testing.T) { gatewayUploadReceiptsSuite(t, state.NewMemStore()) }
func TestGatewayUploadReceiptsPG(t *testing.T) {
	st, _ := pgStore(t)
	gatewayUploadReceiptsSuite(t, st)
}

func TestGatewayCopyReceiptsMem(t *testing.T) {
	gatewayWriteReceiptsSuite(t, state.NewMemStore(), true)
}
func TestGatewayCopyReceiptsPG(t *testing.T) {
	st, _ := pgStore(t)
	gatewayWriteReceiptsSuite(t, st, true)
}

type gatewayUploadStore interface {
	accountingStore
	state.ObjectTrackedGatewayCopyStore
	state.ObjectCapacityStore
}

func gatewayUploadReceiptsSuite(t *testing.T, st gatewayUploadStore) {
	gatewayWriteReceiptsSuite(t, st, false)
}

func gatewayWriteReceiptsSuite(t *testing.T, st gatewayUploadStore, copy bool) {
	ctx := context.Background()
	b, _ := seedAccounting(t, st)
	c := state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "credential", Key: "same-key", Bytes: 10, Status: "pending"}
	p := accountingPolicy()
	begin := st.BeginTrackedGatewayUpload
	origin := "gateway"
	if copy {
		begin = st.BeginTrackedGatewayCopy
		origin = "gateway_copy"
		c.SourceKey = "source"
		c.SourceETag = `"source"`
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := begin(ctx, c, p)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, state.ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("duplicate admission", wins.Load())
	}
	saved, err := st.GetObjectUploadReceipt(ctx, b.AccountID, b.AppID, "", c.SubjectID, c.ID)
	if err != nil || saved.Origin != origin || saved.WritePhase != state.ObjectUploadPrepared || saved.RouteID != "" {
		t.Fatal(saved, err)
	}
	if saved.SourceKey != c.SourceKey || saved.SourceETag != c.SourceETag {
		t.Fatal("source identity not durable", saved)
	}
	if copy {
		bad := c
		bad.ID = uuid.NewString()
		bad.SourceETag = ""
		if _, err = begin(ctx, bad, p); !errors.Is(err, state.ErrConflict) {
			t.Fatal("copy admitted without source proof", err)
		}
	}
	usage, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Authorizations != 1 || usage.Buckets[0].GrantedBytes != 10 {
		t.Fatal(usage, err)
	}
	foreign := c
	foreign.ID = uuid.NewString()
	foreign.AppID = uuid.NewString()
	if _, err = begin(ctx, foreign, p); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign bucket", err)
	}
	invalid := c
	invalid.ID = uuid.NewString()
	invalid.IdempotencyKey = "client-idem"
	if _, err = begin(ctx, invalid, p); !errors.Is(err, state.ErrConflict) {
		t.Fatal("gateway silently deduplicated client semantics", err)
	}
	if err = st.SettleObjectWrite(ctx, b.AccountID, b.ID, c.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("legacy settle bypass", err)
	}
	// Another S3 PUT to the same key is an independent intent, while the key grant keeps its maximum.
	next := c
	next.ID = uuid.NewString()
	next.Bytes = 5
	if _, err = begin(ctx, next, p); err != nil {
		t.Fatal(err)
	}
	wins.Store(0)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID)
			if e == nil {
				wins.Add(1)
			} else if !errors.Is(e, state.ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("duplicate dispatch", wins.Load())
	}
	j, err := st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "wait")
	if err != nil || j.State != "waiting" || j.PendingWrites != 2 {
		t.Fatal(j, err)
	}
	c.Status = "completed"
	c.ETag = "etag"
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	next.Status = "failed"
	next.ErrorCode = "dispatch_failed"
	if _, err = st.FinishTrackedObjectUpload(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, next.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("late dispatch", err)
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
		t.Fatal(j, err)
	}
	if _, err = st.FinishObjectCapacityReconciliation(ctx, j.ID, "scan", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = st.GetObjectUploadReceipt(ctx, b.AccountID, b.AppID, "", c.SubjectID, c.ID); err != nil {
		t.Fatal("receipt lost on rebase", err)
	}
	next = c
	next.ID = uuid.NewString()
	next.Key = "reuse"
	next.Bytes = 100
	next.Status = "pending"
	next.ETag = ""
	if _, err = begin(ctx, next, p); err != nil {
		t.Fatal("capacity not reusable", err)
	}
	rejected := next
	rejected.ID = uuid.NewString()
	rejected.Key = "over-limit"
	if _, err = begin(ctx, rejected, p); !errors.Is(err, state.ErrObjectCapacity) {
		t.Fatal(err)
	}
	if _, err = st.GetObjectUploadReceipt(ctx, b.AccountID, b.AppID, "", c.SubjectID, rejected.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed admission left intent", err)
	}
	usage, err = st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Authorizations != 3 || usage.Buckets[0].GrantedBytes != 100 {
		t.Fatal("billing/admission not atomic", usage, err)
	}
}
