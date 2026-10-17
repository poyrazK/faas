package s3gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func recoveryDiskFixture(t *testing.T) (*objectnode.Store, *DiskHandler) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "node")
	s, err := objectnode.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err = objectnode.EnableReplication(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	s, err = objectnode.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err = s.EnableRecoveryRetention(); err != nil {
		t.Fatal(err)
	}
	h, err := NewDiskHandler(DiskConfig{Store: s, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1", MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	return s, h
}

func TestGatewaySettlementQueuesOnlyCommittedState(t *testing.T) {
	for _, mode := range []string{"committed", "lost acknowledgement", "settlement failure"} {
		t.Run(mode, func(t *testing.T) {
			var owner atomic.Pointer[gatewayReceiptStore]
			var attempts atomic.Int64
			h, st, s := gatewayAdmissionFixture(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Has(objectrecovery.Query) {
						attempts.Add(1)
						store := owner.Load()
						var a objectrecovery.Settlement
						body, _ := io.ReadAll(r.Body)
						r.Body = io.NopCloser(bytes.NewReader(body))
						if json.Unmarshal(body, &a) != nil {
							t.Error("invalid settlement payload")
						}
						c, err := store.GetObjectUploadReceipt(r.Context(), store.bucket.AccountID, store.bucket.AppID, "", store.credential.ID, a.ID)
						if err != nil || c.Status != "completed" || c.WritePhase != state.ObjectUploadSettled {
							t.Error("acknowledgement preceded committed customer result", c, err)
						}
						if mode == "lost acknowledgement" {
							w.WriteHeader(503)
							return
						}
					}
					next.ServeHTTP(w, r)
				})
			}, true)
			owner.Store(st)
			st.finishLost = mode == "settlement failure"
			w := httptest.NewRecorder()
			h.ServeHTTP(w, admissionGatewayRequest(t, "PUT", "key", []byte("body")))
			if mode != "settlement failure" && w.Code != 200 || mode == "settlement failure" && w.Code < 500 {
				t.Fatal(mode, w.Code, w.Body.String())
			}
			proof, err := s.Receipt("physical", "key", w.Header().Get("X-Gregale-Upload-ID"))
			if err != nil || proof.SettledAt != nil || attempts.Load() != 0 {
				t.Fatal("acknowledgement ran during customer request", proof, err, attempts.Load())
			}
			jobs, err := st.ClaimObjectNodeSettlements(t.Context(), uuid.NewString(), api.ObjectNodeSettlementBatch)
			want := 1
			if mode == "settlement failure" {
				want = 0
			}
			if err != nil || len(jobs) != want {
				t.Fatal("terminal queue disagrees with owner", jobs, err)
			}
			if want == 0 {
				return
			}
			backend, err := h.registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			job := jobs[0]
			err = backend.Provider.(*objectstorage.S3).AcknowledgeOwnedSettlement(t.Context(), job.Settlement)
			if mode == "lost acknowledgement" {
				if err == nil {
					t.Fatal("unavailable acknowledgement accepted")
				}
				if err = st.RetryObjectNodeSettlement(t.Context(), job.ID, job.LeaseToken, "unavailable", api.ObjectNodeSettlementRetryMin); err != nil {
					t.Fatal(err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err = st.FinishObjectNodeSettlement(t.Context(), job.ID, job.LeaseToken); err != nil {
					t.Fatal(err)
				}
			}
			proof, err = s.Receipt("physical", "key", job.ID)
			if err != nil || (proof.SettledAt != nil) != (mode == "committed") || attempts.Load() != 1 {
				t.Fatal("wrong durable proof protection", proof, err, attempts.Load())
			}

		})
	}
}

func TestDiskRecoverySettlementRequiresExactSignedPayload(t *testing.T) {
	s, h := recoveryDiskFixture(t)
	id := objectrecovery.NewID()
	o, err := s.Put(t.Context(), "test-bucket", "key", strings.NewReader("body"), 4, objectnode.PutOptions{Receipt: id})
	if err != nil {
		t.Fatal(err)
	}
	a := objectrecovery.Settlement{ID: id, Bucket: "test-bucket", Key: "key", Kind: "put", Status: "committed", Size: 4, ETag: o.ETag}
	encoded, _ := json.Marshal(a)
	for _, mode := range []string{"unsigned", "tampered", "unsigned payload", "duplicate field", "unknown field", "trailing JSON", "crossed selector", "extra query", "object path", "wrong key"} {
		t.Run(mode, func(t *testing.T) {
			path, body := "/?gregale-recovery=settle", append([]byte(nil), encoded...)
			switch mode {
			case "duplicate field":
				body = bytes.Replace(body, []byte(`"kind":"put"`), []byte(`"kind":"put","kind":"put"`), 1)
			case "unknown field":
				body = append(body[:len(body)-1], []byte(`,"extra":true}`)...)
			case "trailing JSON":
				body = append(body, []byte(`{}`)...)
			case "extra query":
				path += "&uploads="
			case "object path":
				path = "/test-bucket/key?gregale-recovery=settle"
			case "wrong key":
				body = bytes.Replace(body, []byte(`"key":"key"`), []byte(`"key":"other"`), 1)
			}
			r := signedDurabilityRequest(t, t.Context(), http.MethodPost, path, body, http.Header{"Content-Type": {"application/json"}})
			switch mode {
			case "unsigned":
				r.Header.Del("Authorization")
			case "tampered":
				r.Body = io.NopCloser(bytes.NewReader(bytes.ReplaceAll(encoded, []byte(`"key":"key"`), []byte(`"key":"bad"`))))
			case "unsigned payload":
				r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
			case "crossed selector":
				r.Header.Set(objectstorage.OwnedNodeReceiptHeader, id)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code < 400 {
				t.Fatal("invalid settlement accepted", mode, w.Code)
			}
			proof, err := s.Receipt("test-bucket", "key", id)
			if err != nil || proof.SettledAt != nil {
				t.Fatal("invalid request acknowledged proof", proof, err)
			}
		})
	}
	setDiskAdmissionPressure(t, s, true)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedDurabilityRequest(t, t.Context(), http.MethodPost, "/?gregale-recovery=settle", encoded, http.Header{"Content-Type": {"application/json"}}))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("recovery acknowledgement blocked under write pressure", w.Code, w.Body.String())
	}
	proof, err := s.Receipt("test-bucket", "key", id)
	if err != nil || proof.SettledAt == nil {
		t.Fatal(proof, err)
	}
}
