package state_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"github.com/onebox-faas/faas/pkg/state"
)

type nodeSettlementTestStore interface {
	accountingStore
	state.ObjectTrackedGatewayUploadStore
	state.ObjectNodeSettlementStore
	state.ObjectMultipartUploadStore
	state.ObjectMultipartTransferStore
	state.ObjectMultipartCompletionStore
	state.ObjectUploadRouteStore
}

func TestObjectNodeSettlementOutboxMem(t *testing.T) {
	st := state.NewMemStore()
	now := time.Now().UTC()
	st.SetClockForTest(func() time.Time { return now })
	nodeSettlementContract(t, st, func() { now = now.Add(api.ObjectNodeSettlementLease + time.Second) })
}

func TestObjectNodeSettlementOutboxPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	nodeSettlementContract(t, st, func() {
		if _, err := pool.Exec(ctx, `UPDATE object_node_settlement_outbox SET retry_at=clock_timestamp()-interval '1 second', lease_until=CASE WHEN lease_token IS NULL THEN NULL ELSE clock_timestamp()-interval '1 second' END WHERE ready_at IS NOT NULL`); err != nil {
			t.Fatal(err)
		}
	})
}

func beginNodePut(t *testing.T, st nodeSettlementTestStore, b state.ObjectBucket, enabled bool) state.ObjectUploadCompletion {
	t.Helper()
	policy := accountingPolicy()
	policy.MaxAccountKeys = 100
	c, err := st.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: objectrecovery.NewID(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "owner", Key: uuid.NewString(), Bytes: 1, Status: "pending", QueueNodeSettlement: enabled}, policy)
	if err != nil || c.QueueNodeSettlement {
		t.Fatal("admission or transient enrollment", c, err)
	}
	return c
}

func completeNodePut(t *testing.T, st nodeSettlementTestStore, c state.ObjectUploadCompletion) state.ObjectUploadCompletion {
	t.Helper()
	c, err := st.DispatchTrackedObjectUpload(t.Context(), c.AccountID, c.BucketID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag = "completed", `"exact"`
	c, err = st.FinishTrackedObjectUpload(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func claimNodeJobs(t *testing.T, st state.ObjectNodeSettlementStore, count int) []state.ObjectNodeSettlement {
	t.Helper()
	jobs, err := st.ClaimObjectNodeSettlements(t.Context(), uuid.NewString(), api.ObjectNodeSettlementBatch)
	if err != nil || len(jobs) != count {
		t.Fatalf("claim: got %d jobs, want %d: %v", len(jobs), count, err)
	}
	return jobs
}

func nodeSettlementContract(t *testing.T, st nodeSettlementTestStore, expire func()) {
	t.Helper()
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	c := beginNodePut(t, st, b, true)
	claimNodeJobs(t, st, 0)
	stats, err := st.ObjectNodeSettlementBacklog(ctx)
	if err != nil || stats.Waiting != 1 || stats.Ready != 0 {
		t.Fatal("waiting enrollment missing", stats, err)
	}
	c.Status, c.ErrorCode = "failed", "dispatch_failed"
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	stats, _ = st.ObjectNodeSettlementBacklog(ctx)
	if stats.Waiting != 0 || stats.Ready != 0 {
		t.Fatal("prepared failure retained a delivery job", stats)
	}
	// Opt-out and legacy admission never acquire a delivery job, even if a
	// terminal caller supplies the transient flag later.
	c = completeNodePut(t, st, beginNodePut(t, st, b, false))
	c.QueueNodeSettlement = true
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	claimNodeJobs(t, st, 0)
	bad := state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "owner", Key: "legacy", Status: "pending", QueueNodeSettlement: true}
	if _, err = st.BeginTrackedGatewayUpload(ctx, bad, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("legacy identity enrolled", err)
	}
	// Unknown/dispatched outcomes remain waiting and cannot be acknowledged.
	c = beginNodePut(t, st, b, true)
	c, err = st.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimNodeJobs(t, st, 0)
	c.Status, c.ErrorCode = "failed", "provider_write_rejected"
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	job := claimNodeJobs(t, st, 1)[0]
	if job.Settlement.Status != "rejected" || job.Settlement.ETag != "" || job.Settlement.Size != 1 {
		t.Fatal("rejected evidence changed", job)
	}
	if err = st.FinishObjectNodeSettlement(ctx, job.ID, job.LeaseToken); err != nil {
		t.Fatal(err)
	}

	c = completeNodePut(t, st, beginNodePut(t, st, b, true))
	job = claimNodeJobs(t, st, 1)[0]
	if job.ID != c.ID || job.BackendID != b.BackendID || job.BackendFingerprint != b.BackendFingerprint || job.PhysicalName != b.PhysicalName || job.Settlement.Bucket != b.PhysicalName || job.Settlement.ETag != c.ETag || job.Settlement.Key != c.Key || job.Attempts != 1 || job.ReadyAt.IsZero() {
		t.Fatal("original placement or terminal evidence lost", job)
	}
	claimNodeJobs(t, st, 0)
	if err = st.FinishObjectNodeSettlement(ctx, job.ID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("foreign lease deleted job", err)
	}
	if err = st.RetryObjectNodeSettlement(ctx, job.ID, job.LeaseToken, "unavailable", api.ObjectNodeSettlementRetryMin); err != nil {
		t.Fatal(err)
	}
	claimNodeJobs(t, st, 0) // Persisted cooldown survives another worker.
	expire()
	next := claimNodeJobs(t, st, 1)[0]
	if next.Attempts != 2 || next.Settlement != job.Settlement || next.LastError != "unavailable" {
		t.Fatal("retry lost immutable job", next)
	}
	expire() // Simulate a worker crash after claiming.
	if err = st.FinishObjectNodeSettlement(ctx, next.ID, next.LeaseToken); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired worker deleted job", err)
	}
	successor := claimNodeJobs(t, st, 1)[0]
	if err = st.RetryObjectNodeSettlement(ctx, next.ID, next.LeaseToken, "configuration", time.Hour); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker changed successor retry", err)
	}
	if err = st.FinishObjectNodeSettlement(ctx, successor.ID, successor.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	claimNodeJobs(t, st, 0) // Terminal replay cannot resurrect a delivered job.

	// Multiple workers share a bounded claim without duplicating delivery work.
	for range 17 {
		completeNodePut(t, st, beginNodePut(t, st, b, true))
	}
	var wg sync.WaitGroup
	claimed := make(chan state.ObjectNodeSettlement, 32)
	for range 4 {
		wg.Go(func() {
			jobs, err := st.ClaimObjectNodeSettlements(ctx, uuid.NewString(), 8)
			if err != nil || len(jobs) > 8 {
				t.Error("bounded claim", len(jobs), err)
			}
			for _, job := range jobs {
				claimed <- job
			}
		})
	}
	wg.Wait()
	close(claimed)
	seen := map[string]bool{}
	for job := range claimed {
		if seen[job.ID] {
			t.Fatal("two workers claimed the same job", job.ID)
		}
		seen[job.ID] = true
		if err = st.FinishObjectNodeSettlement(ctx, job.ID, job.LeaseToken); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 17 {
		t.Fatal("due jobs missing", len(seen))
	}
}

func TestObjectNodeSettlementMultipartMem(t *testing.T) {
	nodeSettlementMultipartContract(t, state.NewMemStore())
}

func TestObjectNodeSettlementMultipartPG(t *testing.T) {
	st, _ := pgStore(t)
	nodeSettlementMultipartContract(t, st)
}

func nodeSettlementMultipartContract(t *testing.T, st nodeSettlementTestStore) {
	t.Helper()
	for _, outcome := range []string{"committed", "rejected", "aborted"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := t.Context()
			b, _ := seedAccounting(t, st)
			u, err := st.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: objectrecovery.NewID(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "multipart", QueueNodeSettlement: true, ExpiresAt: time.Now().Add(time.Hour)}, 10)
			if err != nil || u.QueueNodeSettlement {
				t.Fatal(u, err)
			}
			if _, err = st.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
				t.Fatal(err)
			}
			if err = st.ActivateObjectMultipartUpload(ctx, u.ID, "init", objectmultipart.NativeUploadID(u.ID)); err != nil {
				t.Fatal(err)
			}
			u, err = st.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if outcome != "aborted" {
				if err = st.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "part", 1, 3, 100, accountingPolicy()); err != nil {
					t.Fatal(err)
				}
				if err = st.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "part"); err != nil {
					t.Fatal(err)
				}
				u, err = st.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "rejected" {
					u.CompletionConditions.IfNoneMatch = "*"
				}
				u, err = st.PrepareObjectMultipartCompletion(ctx, u, "complete", 3, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}, accountingPolicy())
				if err != nil {
					t.Fatal(err)
				}
				if err = st.DispatchObjectMultipartCompletion(ctx, u); err != nil {
					t.Fatal(err)
				}
			}
			if outcome == "committed" {
				u, err = st.FinishObjectMultipartCompletion(ctx, u, state.ObjectMultipartCompletionResult{ETag: `"assembled"`})
			} else {
				if outcome == "rejected" {
					if err = st.RejectObjectMultipartCompletionResult(ctx, u, state.ObjectMultipartCompletionResult{}, "precondition_failed"); err != nil {
						t.Fatal(err)
					}
				}
				claimNodeJobs(t, st, 0) // Rejection alone cannot release native parts.
				u, err = st.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false)
				if err != nil {
					t.Fatal(err)
				}
				err = st.FinishVerifiedObjectMultipartAbort(ctx, u.ID, u.LeaseToken)
			}
			if err != nil {
				t.Fatal(err)
			}
			job := claimNodeJobs(t, st, 1)[0]
			if job.Settlement.Status != outcome || job.Kind != "multipart" || outcome != "aborted" && len(job.Settlement.ManifestHash) != 64 || outcome == "aborted" && (job.Settlement.Size != 0 || job.Settlement.ManifestHash != "") {
				t.Fatal("multipart evidence changed", job)
			}
			if err = st.FinishObjectNodeSettlement(ctx, job.ID, job.LeaseToken); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestObjectNodeSettlementOutboxAtomicPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	// A failed enrollment cannot leave an admitted write without its job.
	_, err := pool.Exec(ctx, `CREATE FUNCTION reject_node_job() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected outbox failure'; END $$;
CREATE TRIGGER reject_node_job BEFORE INSERT OR UPDATE ON object_node_settlement_outbox FOR EACH ROW EXECUTE FUNCTION reject_node_job()`)
	if err != nil {
		t.Fatal(err)
	}
	c := state.ObjectUploadCompletion{ID: objectrecovery.NewID(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "owner", Key: "atomic", Bytes: 1, Status: "pending", QueueNodeSettlement: true}
	if _, err = st.BeginTrackedGatewayUpload(ctx, c, accountingPolicy()); err == nil {
		t.Fatal("admission ignored failed enrollment")
	}
	usage, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Buckets[0].GrantedBytes != 0 || usage.Authorizations != 0 {
		t.Fatal("failed enrollment spent capacity", usage, err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_node_job ON object_node_settlement_outbox`); err != nil {
		t.Fatal(err)
	}
	c, err = st.BeginTrackedGatewayUpload(ctx, c, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	c, err = st.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE TRIGGER reject_node_job BEFORE UPDATE ON object_node_settlement_outbox FOR EACH ROW EXECUTE FUNCTION reject_node_job()`); err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag = "completed", `"exact"`
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err == nil {
		t.Fatal("owner committed without its durable acknowledgement")
	}
	saved, err := st.GetObjectUploadReceipt(ctx, b.AccountID, b.AppID, "", "owner", c.ID)
	if err != nil || saved.Status != "pending" || saved.WritePhase != state.ObjectUploadDispatched {
		t.Fatal("settlement rollback lost pending owner", saved, err)
	}
	if _, err = st.ReadTrackedObjectUploadMutation(ctx, saved); err != nil {
		t.Fatal("settlement rollback retired original binding", err)
	}
	claimNodeJobs(t, st, 0)
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_node_job ON object_node_settlement_outbox`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	// A fresh state owner sees the committed job without process memory.
	restarted := state.NewPgStore(pool)
	job := claimNodeJobs(t, restarted, 1)[0]
	if job.ID != c.ID || job.Settlement.ETag != c.ETag {
		t.Fatal("restart lost terminal evidence", job)
	}
	if err = restarted.FinishObjectNodeSettlement(ctx, job.ID, job.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	claimNodeJobs(t, restarted, 0)
}
