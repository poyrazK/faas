package object

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func queuedNodePut(t *testing.T, st *state.MemStore, registry *objectstorage.Registry, b state.ObjectBucket, subject string) state.ObjectUploadCompletion {
	t.Helper()
	c, err := st.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: objectrecovery.NewID(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: subject, Key: uuid.NewString(), Bytes: 1, Status: "pending", QueueNodeSettlement: true}, registry.Accounting)
	if err != nil {
		t.Fatal(err)
	}
	c, err = st.DispatchTrackedObjectUpload(t.Context(), c.AccountID, c.BucketID, c.ID)
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

func TestOwnedSettlementDeliveryDefersWithoutChangingOutcome(t *testing.T) {
	for _, scenario := range []string{"changed placement", "missing configuration", "missing proof", "malformed reply"} {
		t.Run(scenario, func(t *testing.T) {
			var requests atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if scenario == "missing proof" {
					w.WriteHeader(http.StatusNotFound)
				} else {
					_, _ = w.Write([]byte(`{"version":1}`))
				}
			}))
			defer upstream.Close()
			st, registry, bucket, subject := receiptRecoverySettlementFixture(t, upstream.URL, true)
			c := queuedNodePut(t, st, registry, bucket, subject)
			var offset atomic.Int64
			st.SetClockForTest(func() time.Time { return time.Now().Add(time.Duration(offset.Load())) })
			selected := registry
			if scenario == "changed placement" {
				replacement := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					requests.Add(1)
					t.Error("saved job was retargeted")
				}))
				defer replacement.Close()
				_, selected, _, _ = receiptRecoverySettlementFixture(t, replacement.URL, true)
			} else if scenario == "missing configuration" {
				selected = nil
			}
			service := Service{Host: receiptRecoveryHost{store: st, registry: selected}}
			var outcome string
			if err := service.ReconcileObjectNodeSettlements(t.Context(), func(operation, result string) {
				if operation != "settlement" {
					t.Error(operation)
				}
				outcome = result
			}); err != nil {
				t.Fatal(err)
			}
			wantCode, wantRequests := "unavailable", int64(1)
			if scenario == "changed placement" || scenario == "missing configuration" {
				wantCode, wantRequests = "configuration", 0
			}
			if outcome != wantCode || requests.Load() != wantRequests {
				t.Fatal("unexpected delivery outcome", outcome, requests.Load())
			}
			done, err := st.GetObjectUploadReceipt(t.Context(), c.AccountID, c.AppID, "", subject, c.ID)
			if err != nil || done.Status != "completed" || done.ETag != c.ETag {
				t.Fatal("delivery rewrote customer outcome", done, err)
			}
			metrics := newNodeSettlementMetrics()
			if err = service.observeNodeSettlementBacklog(t.Context(), metrics); err != nil || testutil.ToFloat64(metrics.backlog.WithLabelValues("ready")) != 1 || testutil.ToFloat64(metrics.backlog.WithLabelValues("waiting")) != 0 || testutil.ToFloat64(metrics.oldest) < 0 {
				t.Fatal("deferred backlog missing", err)
			}
			if err = service.ReconcileObjectNodeSettlements(t.Context(), nil); err != nil || requests.Load() != wantRequests {
				t.Fatal("cooldown reset", err, requests.Load())
			}
			offset.Store(int64(2 * time.Hour))
			jobs, err := st.ClaimObjectNodeSettlements(t.Context(), uuid.NewString(), 1)
			if err != nil || len(jobs) != 1 || jobs[0].LastError != wantCode || jobs[0].Settlement.ID != c.ID || jobs[0].Attempts != 2 {
				t.Fatal("deferred evidence lost", jobs, err)
			}
		})
	}
}

func TestOwnedSettlementDeliveryCancellationLeavesRecoverableLeases(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var attempts atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a objectrecovery.Settlement
		if json.NewDecoder(r.Body).Decode(&a) != nil {
			t.Error("invalid saved settlement")
		}
		if attempts.Add(1) == 1 {
			cancel()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(objectrecovery.Acknowledgement{Version: 1, ID: a.ID, Status: a.Status, SettledAt: time.Now().UTC()})
	}))
	defer upstream.Close()
	st, registry, bucket, subject := receiptRecoverySettlementFixture(t, upstream.URL, true)
	for range 3 {
		queuedNodePut(t, st, registry, bucket, subject)
	}
	service := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	if err := service.ReconcileObjectNodeSettlements(ctx, nil); !errors.Is(err, context.Canceled) || attempts.Load() != 1 {
		t.Fatal("cancellation dispatched another job", attempts.Load(), err)
	}
	if err := service.ReconcileObjectNodeSettlements(t.Context(), nil); err != nil || attempts.Load() != 1 {
		t.Fatal("another worker bypassed leases", err, attempts.Load())
	}
	st.SetClockForTest(func() time.Time { return time.Now().Add(api.ObjectNodeSettlementLease + time.Minute) })
	restarted := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	if err := restarted.ReconcileObjectNodeSettlements(t.Context(), nil); err != nil || attempts.Load() != 3 {
		t.Fatal("unstarted jobs could not recover", err, attempts.Load())
	}
	stats, err := st.ObjectNodeSettlementBacklog(t.Context())
	if err != nil || stats.Ready != 0 || stats.Waiting != 0 {
		t.Fatal("delivered jobs retained", stats, err)
	}
}
