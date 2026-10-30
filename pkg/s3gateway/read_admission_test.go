package s3gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type cachedReadStore struct {
	reads    int
	recorded []state.ObjectReadUsage
}

func (s *cachedReadStore) ObjectReadAdmission(_ context.Context, _, _ string, at time.Time, _ api.ObjectStoragePolicy) (state.ObjectReadAdmission, error) {
	s.reads++
	return state.ObjectReadAdmission{Period: state.ObjectStoragePeriod(at), MaxRequests: 10, MaxEgressBytes: 10, MaxAuthorizations: 10}, nil
}

func (s *cachedReadStore) RecordObjectReadUsage(_ context.Context, usage []state.ObjectReadUsage) error {
	s.recorded = append(s.recorded, usage...)
	return nil
}

// adr: 20261030075301
func TestGatewayReadsUseCachedAdmission(t *testing.T) {
	h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, nil)
	h.registry.Accounting.AccountingMode = api.ObjectStorageGatewaySafetyV1
	metrics := &readEgressMetrics{}
	h.requestMetrics = metrics
	reads := &cachedReadStore{}
	h.readAdmitter = objectstorage.NewReadAdmitter(reads, h.registry.Accounting, nil)

	r := httptest.NewRequest(http.MethodGet, "https://s3.gregale.dev/assets/key", nil)
	req := requestContext{bucket: store.bucket, credential: store.credential}
	w := httptest.NewRecorder()
	if !h.admitRead(w, r, &req, "key") || req.read == nil || len(store.admitted) != 0 || reads.reads != 1 {
		t.Fatalf("GET took the locking admission: admitted %v, cached reads %d", store.admitted, reads.reads)
	}
	if !h.recordProviderRequest(w, r, req) || metrics.calls != 0 {
		t.Fatal("a cached read wrote its request count", metrics.calls)
	}
	response := &http.Response{StatusCode: http.StatusOK, ContentLength: 6, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("abcdef"))}
	h.writeGatewayRead(w, r, req, "key", response)
	if w.Code != http.StatusOK || w.Body.String() != "abcdef" || metrics.reserved != 0 {
		t.Fatalf("cached read egress went to the database: %d %q reserved %d", w.Code, w.Body.String(), metrics.reserved)
	}
	if err := h.readAdmitter.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(reads.recorded) != 1 || reads.recorded[0].Requests != 1 || reads.recorded[0].Authorizations != 1 || reads.recorded[0].EgressBytes != 6 {
		t.Fatalf("batched counts = %+v", reads.recorded)
	}

	over := httptest.NewRecorder()
	big := &http.Response{StatusCode: http.StatusOK, ContentLength: 5, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("abcde"))}
	h.writeGatewayRead(over, r, req, "key", big)
	if over.Code != http.StatusPaymentRequired || over.Body.Len() == 0 || strings.Contains(over.Body.String(), "abcde") {
		t.Fatalf("egress past the ceiling = %d %q", over.Code, over.Body.String())
	}

	fenced := requestContext{bucket: store.bucket, credential: store.credential}
	fenced.credential.IssuerAccountID = "member"
	fenced.bucket.OrgID = "company"
	if !h.admitRead(httptest.NewRecorder(), r, &fenced, "fenced-key") || fenced.read != nil || len(store.admitted) != 1 {
		t.Fatalf("an organization-fenced credential skipped the locking admission: %v", store.admitted)
	}

	h.registry.Accounting.AccountingMode = ""
	legacy := requestContext{bucket: store.bucket, credential: store.credential}
	if !h.admitRead(httptest.NewRecorder(), r, &legacy, "legacy-key") || legacy.read != nil || len(store.admitted) != 2 {
		t.Fatalf("provider-report accounting skipped the locking admission: %v", store.admitted)
	}
}
