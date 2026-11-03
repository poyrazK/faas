package s3gateway

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Bug hunt 33: listings wrote straight to the client, so a page of long keys
// counted against neither the monthly egress limit nor the node's transfer
// share.
func TestGatewayListingCountsAsEgress(t *testing.T) {
	page := objectstorage.ObjectPage{}
	for i := range 50 {
		page.Items = append(page.Items, objectstorage.Object{Key: strings.Repeat("k", 900) + string(rune('a'+i%26)), Size: 1})
	}
	for _, tc := range []struct {
		name         string
		mode         string
		reserveError error
		wantStatus   int
		reserved     bool
		delivered    bool
	}{
		{"gateway safety reserves the page", api.ObjectStorageGatewaySafetyV1, nil, http.StatusOK, true, false},
		{"gateway safety refuses at the limit", api.ObjectStorageGatewaySafetyV1, state.ErrObjectBudget, http.StatusPaymentRequired, false, false},
		{"legacy records the page", "", nil, http.StatusOK, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, nil)
			h.registry.Accounting.AccountingMode = tc.mode
			metrics := &readEgressMetrics{reserveError: tc.reserveError}
			h.requestMetrics = metrics
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "https://s3.gregale.dev/assets?list-type=2", nil)
			h.writeListingXML(w, r, requestContext{bucket: store.bucket, requestID: "req"}, encodedObjectsResult(store.bucket.Name, r.URL.Query(), 1000, page))
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d %s, want %d", w.Code, w.Body.String(), tc.wantStatus)
			}
			size := int64(w.Body.Len())
			if tc.reserved && (metrics.reserved != size || size < 40_000) {
				t.Fatalf("reserved %d bytes for a %d-byte page", metrics.reserved, size)
			}
			if tc.delivered && metrics.delivered != size {
				t.Fatalf("recorded %d bytes for a %d-byte page", metrics.delivered, size)
			}
			if !tc.reserved && metrics.reserved != 0 || !tc.delivered && metrics.delivered != 0 {
				t.Fatalf("metrics = %+v", metrics)
			}
		})
	}
}

// ListMultipartUploads and the bucket-level reads are admitted like the
// other listings, so the monthly limits and organization fence apply.
func TestGatewayBucketReadsAreAdmitted(t *testing.T) {
	h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, nil)
	h.multipartStore = newGatewayMultipartStore()
	for _, target := range []struct{ method, url string }{
		{http.MethodGet, "https://s3.gregale.dev/assets?uploads="},
		{http.MethodHead, "https://s3.gregale.dev/assets"},
		{http.MethodGet, "https://s3.gregale.dev/assets?location="},
		{http.MethodGet, "https://s3.gregale.dev/"},
	} {
		store.admitted = nil
		w := httptest.NewRecorder()
		h.ServeHTTP(w, signedGatewayRequest(t, target.method, target.url, nil, "UNSIGNED-PAYLOAD"))
		if w.Code != http.StatusOK || !slices.Contains(store.admitted, "__list__") {
			t.Errorf("%s %s = %d, admitted %v; want 200 and an admission", target.method, target.url, w.Code, store.admitted)
		}
	}
}
