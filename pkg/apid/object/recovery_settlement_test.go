package object

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOwnedRecoveryAcknowledgementFollowsGatewaySettlement(t *testing.T) {
	for _, mode := range []string{"committed", "rejected", "absent", "unavailable", "lost acknowledgement"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "node")
			node, err := objectnode.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			if err = node.CreateBucket("physical"); err != nil {
				t.Fatal(err)
			}
			_ = node.Close()
			if _, err = objectnode.EnableReplication(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			node, err = objectnode.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = node.Close() }()
			if _, err = node.EnableRecoveryRetention(); err != nil {
				t.Fatal(err)
			}
			native, err := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: node, AccessKeyID: "node-key", SecretAccessKey: "node-secret", Region: "us-east-1", MinFreeBytes: 1})
			if err != nil {
				t.Fatal(err)
			}
			var st *state.MemStore
			var bucket state.ObjectBucket
			var subject string
			var setupMu sync.Mutex
			var nativeMu sync.RWMutex
			var attempts atomic.Int64
			var lose atomic.Bool
			lose.Store(mode == "lost acknowledgement" || mode == "unavailable")
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nativeMu.RLock()
				defer nativeMu.RUnlock()
				if r.URL.Query().Has(objectrecovery.Query) {
					setupMu.Lock()
					store, b, owner := st, bucket, subject
					setupMu.Unlock()
					attempts.Add(1)
					body, _ := io.ReadAll(r.Body)
					r.Body = io.NopCloser(strings.NewReader(string(body)))
					var a objectrecovery.Settlement
					if json.Unmarshal(body, &a) != nil {
						t.Error("invalid acknowledgement")
					}
					c, e := store.GetObjectUploadReceipt(r.Context(), b.AccountID, b.AppID, "", owner, a.ID)
					if e != nil || c.Status == "pending" || c.WritePhase != state.ObjectUploadSettled {
						t.Error("node acknowledgement preceded owner commit", c, e)
					}
					if lose.Load() {
						if mode == "lost acknowledgement" {
							recorder := httptest.NewRecorder()
							native.ServeHTTP(recorder, r)
							if recorder.Code != http.StatusOK {
								t.Error("node did not persist acknowledgement", recorder.Code, recorder.Body.String())
							}
						}
						w.WriteHeader(503)
						return
					}
				}
				native.ServeHTTP(w, r)
			}))
			defer upstream.Close()
			var registry *objectstorage.Registry
			setupMu.Lock()
			st, registry, bucket, subject = receiptRecoverySettlementFixture(t, upstream.URL, true)
			setupMu.Unlock()
			c, err := st.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: objectstorage.NewWriteReceiptID(), AccountID: bucket.AccountID, AppID: bucket.AppID, BucketID: bucket.ID, SubjectID: subject, Key: "key", Bytes: 4, Status: "pending", QueueNodeSettlement: true}, registry.Accounting)
			if err != nil {
				t.Fatal(err)
			}
			if c, err = st.DispatchTrackedObjectUpload(t.Context(), c.AccountID, c.BucketID, c.ID); err != nil {
				t.Fatal(err)
			}
			if mode != "absent" {
				options := objectnode.PutOptions{Receipt: c.ID}
				if mode == "rejected" {
					if _, err = node.Put(t.Context(), "physical", "key", strings.NewReader("live"), 4, objectnode.PutOptions{}); err != nil {
						t.Fatal(err)
					}
					options.IfNoneMatch = "*"
				}
				_, err = node.Put(t.Context(), "physical", "key", strings.NewReader("body"), 4, options)
				if mode == "rejected" && !errors.Is(err, objectnode.ErrPrecondition) || mode != "rejected" && err != nil {
					t.Fatal(err)
				}
			}
			var offset atomic.Int64
			offset.Store(int64(time.Hour))
			st.SetClockForTest(func() time.Time { return time.Now().Add(time.Duration(offset.Load())) })
			service := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
			if err = service.ReconcileObjectUploads(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			done, err := st.GetObjectUploadReceipt(t.Context(), c.AccountID, c.AppID, "", subject, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if attempts.Load() != 0 {
				t.Fatal("acknowledgement ran in write recovery")
			}
			if mode == "committed" {
				if _, err = st.ClaimObjectBucket(t.Context(), bucket.AccountID, bucket.AppID, bucket.ID, "delete", "deleting"); err != nil {
					t.Fatal(err)
				}
				if err = st.FinishObjectBucket(t.Context(), bucket.ID, "delete", "deleted"); err != nil {
					t.Fatal(err)
				}
			}
			if err = service.ReconcileObjectNodeSettlements(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			if mode == "absent" {
				if attempts.Load() != 0 || done.Status != "pending" || done.WritePhase != state.ObjectUploadDispatched {
					t.Fatal("missing evidence was acknowledged", done, attempts.Load())
				}
				return
			}
			proof, err := node.Receipt("physical", "key", c.ID)
			if err != nil || attempts.Load() != 1 || done.WritePhase != state.ObjectUploadSettled || done.Status == "pending" || (proof.SettledAt == nil) != (mode == "unavailable") {
				t.Fatal("gateway or node settlement lost", done, proof, attempts.Load(), err)
			}
			if mode == "lost acknowledgement" || mode == "unavailable" {
				first := proof.SettledAt
				if err = service.ReconcileObjectNodeSettlements(t.Context(), nil); err != nil || attempts.Load() != 1 {
					t.Fatal("cooldown was reset", err, attempts.Load())
				}
				stats, e := st.ObjectNodeSettlementBacklog(t.Context())
				if e != nil || stats.Ready != 1 {
					t.Fatal("failed delivery lost its job", stats, e)
				}
				lose.Store(false)
				nativeMu.Lock()
				err = node.Close()
				if err == nil {
					node, err = objectnode.Open(root)
				}
				if err == nil {
					native, err = s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: node, AccessKeyID: "node-key", SecretAccessKey: "node-secret", Region: "us-east-1", MinFreeBytes: 1})
				}
				nativeMu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				offset.Add(int64(time.Minute))
				// Rebuild the worker with new enrollment disabled. Saved jobs
				// still drain against the exact original backend fingerprint.
				_, drainRegistry, _, _ := receiptRecoverySettlementFixture(t, upstream.URL, false)
				restarted := Service{Host: receiptRecoveryHost{store: st, registry: drainRegistry}}
				if err = restarted.ReconcileObjectNodeSettlements(t.Context(), nil); err != nil {
					t.Fatal(err)
				}
				proof, err = node.Receipt("physical", "key", c.ID)
				if err != nil || proof.SettledAt == nil || first != nil && !first.Equal(*proof.SettledAt) || attempts.Load() != 2 {
					t.Fatal("retry changed or lost node acknowledgement", proof, err, attempts.Load())
				}
			}
			stats, e := st.ObjectNodeSettlementBacklog(t.Context())
			if e != nil || stats.Ready != 0 || stats.Waiting != 0 {
				t.Fatal("delivered job was retained", stats, e)
			}
			if err = service.ReconcileObjectNodeSettlements(t.Context(), nil); err != nil || attempts.Load() > 2 {
				t.Fatal("delivered job replayed", err, attempts.Load())
			}

		})
	}
}
