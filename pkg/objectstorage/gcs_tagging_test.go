package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/api/option"
)

func newTaggingGCS(t *testing.T, handler http.HandlerFunc) *GCS {
	t.Helper()
	native := httptest.NewServer(handler)
	t.Cleanup(native.Close)
	client, err := storage.NewClient(t.Context(), option.WithEndpoint(native.URL+"/"), option.WithoutAuthentication(), storage.WithDisabledClientMetrics())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return testGCS(native.URL, &googleGCSStore{client: client})
}

func TestGCSTaggingReservesEveryNativeRequest(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		for _, ceiling := range []int64{1, 2} {
			t.Run(method+"/ceiling"+strconv.FormatInt(ceiling, 10), func(t *testing.T) {
				f := newUploadFixture(t)
				b, err := f.store.GetObjectBucket(t.Context(), f.account.ID, f.app.ID, f.route.BucketID)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.store.ClaimObjectInventory(t.Context(), b.ID, "initial"); err != nil {
					t.Fatal(err)
				}
				if err = f.store.FinishObjectInventory(t.Context(), b.ID, "initial", 0, 0); err != nil {
					t.Fatal(err)
				}
				calls, operations, mutations := 0, 0, 0
				p := newTaggingGCS(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					meta := map[string]string{"owner": "customer"}
					metaversion := "1"
					if r.Method == http.MethodPatch {
						if mutations != 1 {
							t.Error("native patch ran without writer")
						}
						var patch struct{ Metadata map[string]string }
						if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
							t.Error(err)
						}
						for key, value := range patch.Metadata {
							meta[key] = value
						}
						metaversion = "2"
					} else if r.Method != http.MethodGet || mutations != 0 {
						t.Error("unexpected preparation", r.Method, mutations)
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"name": "key", "generation": "11", "metageneration": metaversion, "metadata": meta})
				})
				since := time.Now().UTC().Add(-time.Hour)
				policy := f.registry.Accounting
				policy.AccountingMode, policy.GatewayMeteringSince = api.ObjectStorageGatewaySafetyV1, &since
				policy.MaxMonthlyCostMillicents, policy.MaxMonthlyRequests = 0, ceiling
				svc := TaggingService{Provider: p,
					BeforeOperation: func(ctx context.Context) error {
						operations++
						return f.store.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 0, false, policy)
					},
					BeforeRequest: func(ctx context.Context) error {
						return f.store.ReserveObjectStorageGatewayRequest(ctx, b.ID, time.Now().UTC(), policy)
					},
					Mutate: func(ctx context.Context, call func(context.Context) (api.ObjectTaggingResult, error)) (api.ObjectTaggingResult, error) {
						mutations++
						return call(ctx)
					},
				}
				out, err := svc.Do(t.Context(), b, method, "key", "", map[string]string{"team": "storage"})
				wantCalls, wantMutations := 1, 0
				if method != http.MethodGet && ceiling == 2 {
					wantCalls, wantMutations = 2, 1
				}
				denied := method != http.MethodGet && ceiling == 1
				if denied && !errors.Is(err, state.ErrObjectBudget) || !denied && err != nil {
					t.Fatal("unexpected admission", err)
				}
				if !denied && (method == http.MethodDelete && len(out.Tags) != 0 || method == http.MethodPut && out.Tags["team"] != "storage") {
					t.Fatal("incorrect tag result", out)
				}
				usage, usageErr := f.store.ObjectUsage(t.Context(), b.AccountID, time.Now())
				count := state.SummarizeObjectUsage(usage, policy, time.Now()).RequestCount
				if usageErr != nil || calls != wantCalls || count != int64(calls) || operations != 1 || mutations != wantMutations {
					t.Fatalf("calls=%d count=%d operations=%d mutations=%d usage=%v", calls, count, operations, mutations, usageErr)
				}
			})
		}
	}
}

func TestGCSTaggingPreservesConcurrentMetadataAndReceipt(t *testing.T) {
	for _, change := range []string{"generation", "metageneration"} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			t.Run(change+method, func(t *testing.T) {
				oldReceipt, newReceipt := uuid.NewString(), uuid.NewString()
				current := map[string]string{ReservedUploadReceiptMetadataKey: oldReceipt, "owner": "old"}
				generation, metageneration := "11", "1"
				patches := 0
				p := newTaggingGCS(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodHead {
						w.Header().Set("Content-Length", "7")
						w.Header().Set("ETag", `"etag"`)
						w.Header().Set("X-Goog-Generation", generation)
						w.Header().Set("X-Goog-Meta-"+ReservedUploadReceiptMetadataKey, current[ReservedUploadReceiptMetadataKey])
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if r.Method == http.MethodGet {
						_ = json.NewEncoder(w).Encode(map[string]any{"name": "key", "generation": generation, "metageneration": metageneration, "metadata": current})
						current = map[string]string{ReservedUploadReceiptMetadataKey: newReceipt, "owner": "new"}
						if change == "generation" {
							generation = "22"
						} else {
							metageneration = "2"
						}
						return
					}
					patches++
					if r.Method != http.MethodPatch || r.URL.Query().Get("ifGenerationMatch") != "11" || r.URL.Query().Get("ifMetagenerationMatch") != "1" || r.URL.Query().Has("generation") {
						t.Error("patch did not pin the live generation and metageneration", r.URL)
					}
					var patch struct{ Metadata map[string]string }
					if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || len(patch.Metadata) != 1 {
						t.Error("patch replayed unrelated metadata", patch, err)
					}
					want := ""
					if method == http.MethodPut {
						want = "team=storage"
					}
					if value, ok := patch.Metadata[ReservedObjectTagsMetadataKey]; !ok || value != want {
						t.Error("incorrect tag patch", patch)
					}
					w.WriteHeader(http.StatusPreconditionFailed)
					_, _ = io.WriteString(w, `{"error":{"code":412}}`)
				})
				if _, err := p.ConfirmTrackedObject(t.Context(), "physical", "key", oldReceipt, 7); err != nil {
					t.Fatal("invalid original proof", err)
				}
				var err error
				if method == http.MethodPut {
					err = p.PutObjectTags(t.Context(), "physical", "key", map[string]string{"team": "storage"})
				} else {
					err = p.DeleteObjectTags(t.Context(), "physical", "key")
				}
				if !errors.Is(err, ErrWriteRejected) || !errors.Is(err, ErrConflict) || patches != 1 {
					t.Fatal("conditional rejection was lost or retried", err, patches)
				}
				if _, err := p.ConfirmTrackedObject(t.Context(), "physical", "key", newReceipt, 7); err != nil || current["owner"] != "new" {
					t.Fatal("concurrent replacement lost metadata or recovery proof", err, current)
				}
			})
		}
	}
}

func TestGCSTaggingDoesNotRetryUncertainPatch(t *testing.T) {
	patches := 0
	p := newTaggingGCS(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"name":"key","generation":"11","metageneration":"1"}`)
			return
		}
		patches++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"code":503}}`)
	})
	err := p.PutObjectTags(t.Context(), "physical", "key", nil)
	if patches != 1 || !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrWriteRejected) {
		t.Fatal("uncertain patch was retried or classified as rejected", patches, err)
	}
}

func TestGCSTaggingRejectsInvalidIdentityAcknowledgments(t *testing.T) {
	for _, tc := range []struct {
		name, read, patch string
		mutations         int
	}{
		{name: "missing read generation", read: `{"name":"key","metageneration":"1"}`},
		{name: "missing read metageneration", read: `{"name":"key","generation":"11"}`},
		{name: "wrong read key", read: `{"name":"other","generation":"11","metageneration":"1"}`},
		{name: "wrong acknowledged generation", patch: `{"name":"key","generation":"22","metageneration":"2"}`, mutations: 1},
		{name: "unchanged metageneration", patch: `{"name":"key","generation":"11","metageneration":"1"}`, mutations: 1},
		{name: "wrong acknowledged tags", patch: `{"name":"key","generation":"11","metageneration":"2","metadata":{"gregale-s3-tags":"other=value"}}`, mutations: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patches, mutations := 0, 0
			p := newTaggingGCS(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					body := tc.read
					if body == "" {
						body = `{"name":"key","generation":"11","metageneration":"1"}`
					}
					_, _ = io.WriteString(w, body)
					return
				}
				patches++
				_, _ = io.WriteString(w, tc.patch)
			})
			svc := TaggingService{Provider: p, Mutate: func(ctx context.Context, call func(context.Context) (api.ObjectTaggingResult, error)) (api.ObjectTaggingResult, error) {
				mutations++
				return call(ctx)
			}}
			_, err := svc.Do(t.Context(), state.ObjectBucket{PhysicalName: "physical"}, http.MethodPut, "key", "", nil)
			if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrWriteRejected) || patches != tc.mutations || mutations != tc.mutations {
				t.Fatal("invalid identity accepted or lost writer custody", err, patches, mutations)
			}
		})
	}
}
