package objectstorage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func newPublicRangeHandler(t *testing.T, client *http.Client) (*publicReadHandler, uploadFixture, state.ObjectBucket) {
	t.Helper()
	f := newUploadFixture(t)
	backend := f.registry.backends["test"]
	backend.Provider = publicReadTestProvider{endpoint: "https://storage.test/file"}
	f.registry.backends["test"] = backend
	handler, err := NewPublicReadHandler(PublicReadConfig{Store: f.store, Registry: f.registry, RequestMetrics: f.store, Accounting: f.accounting, AppsDomain: "apps.test", Next: http.NotFoundHandler(), HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := f.store.GetObjectBucket(t.Context(), f.account.ID, f.app.ID, f.route.BucketID)
	if err != nil {
		t.Fatal(err)
	}
	bucket.PublicRead, bucket.ServeAt = true, "/assets"
	h := handler.(*publicReadHandler)
	h.mounts.put("upload-test.apps.test", []state.ObjectBucket{bucket}, time.Now())
	return h, f, bucket
}

func TestPublicReadIfRange(t *testing.T) {
	modified := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, validator, rangeValue string
		ignore                      bool
		status, calls               int
		body                        string
	}{
		{"matching ETag", `"current"`, "bytes=4-", true, 206, 1, "CONTENT"},
		{"stale ETag ignored upstream", `"old"`, "bytes=4-", true, 200, 2, "NEW-CONTENT"},
		{"stale ETag honored upstream", `"old"`, "bytes=4-", false, 200, 1, "NEW-CONTENT"},
		{"weak ETag", `W/"current"`, "bytes=4-", true, 200, 2, "NEW-CONTENT"},
		{"invalid validator", "invalid", "bytes=4-", true, 200, 2, "NEW-CONTENT"},
		{"matching date", modified.Format(http.TimeFormat), "bytes=4-", true, 206, 1, "CONTENT"},
		{"stale date", modified.Add(-time.Hour).Format(http.TimeFormat), "bytes=4-", true, 200, 2, "NEW-CONTENT"},
		{"future date", modified.Add(time.Hour).Format(http.TimeFormat), "bytes=4-", true, 200, 2, "NEW-CONTENT"},
		{"stale unsatisfiable range", `"old"`, "bytes=99-", true, 200, 2, "NEW-CONTENT"},
		{"no Range", `"old"`, "", true, 200, 1, "NEW-CONTENT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: metadataRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 && r.Header.Get("If-Range") != tc.validator || calls == 2 && (r.Header.Get("Range") != "" || r.Header.Get("If-Range") != "") {
					t.Error("range conditions were not forwarded or cleared correctly")
				}
				if tc.ignore {
					r.Header.Del("If-Range")
				}
				w := httptest.NewRecorder()
				w.Header().Set("ETag", `"current"`)
				http.ServeContent(w, r, "file", modified, strings.NewReader("NEW-CONTENT"))
				return w.Result(), nil
			})}
			h, f, bucket := newPublicRangeHandler(t, client)
			r := httptest.NewRequest(http.MethodGet, "https://upload-test.apps.test/assets/file", nil)
			r.Header.Set("Range", tc.rangeValue)
			r.Header.Set("If-Range", tc.validator)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || w.Body.String() != tc.body || calls != tc.calls || f.accounting.calls != tc.calls {
				t.Fatalf("status=%d body=%q provider_calls=%d admissions=%d", w.Code, w.Body.String(), calls, f.accounting.calls)
			}
			metrics, err := f.store.ListObjectStorageProviderRequestMetrics(t.Context(), bucket.BackendID, bucket.BackendFingerprint, state.ObjectStoragePeriod(time.Now()))
			if err != nil || len(metrics) != 1 || metrics[0].RequestCount != int64(tc.calls) || metrics[0].EgressBytes != int64(len(tc.body)) {
				t.Fatal("fallback attempt not metered", metrics, err)
			}
		})
	}
}

func TestPublicReadIfRangeFallbackCachedBudgets(t *testing.T) {
	for _, tc := range []struct {
		name             string
		requests, egress int64
		status, calls    int
		bytes            int64
	}{
		{"full body reserved once", 2, 11, 200, 2, 11},
		{"request ceiling", 1, 11, 429, 1, 0},
		{"full body exceeds egress", 2, 7, 429, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: metadataRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				r.Header.Del("If-Range")
				w := httptest.NewRecorder()
				w.Header().Set("ETag", `"current"`)
				http.ServeContent(w, r, "file", time.Now().Add(-time.Hour), strings.NewReader("NEW-CONTENT"))
				return w.Result(), nil
			})}
			h, _, _ := newPublicRangeHandler(t, client)
			h.registry.Accounting.AccountingMode = api.ObjectStorageGatewaySafetyV1
			since := time.Now().Add(-time.Hour)
			h.registry.Accounting.GatewayMeteringSince = &since
			h.registry.Accounting.MaxMonthlyCostMillicents = 0
			d := roomyDecision()
			d.MaxRequests, d.MaxEgressBytes = tc.requests, tc.egress
			store := &fakeReadStore{decision: d}
			h.readAdmitter = NewReadAdmitter(store, h.registry.Accounting.AnonymousReadPolicy(), nil)
			r := httptest.NewRequest(http.MethodGet, "https://upload-test.apps.test/assets/file", nil)
			r.Header.Set("Range", "bytes=4-")
			r.Header.Set("If-Range", `"old"`)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%q", w.Code, calls, w.Body.String())
			}
			if err := h.readAdmitter.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(store.recorded) != 1 || store.recorded[0].Requests != int64(calls) || store.recorded[0].EgressBytes != tc.bytes {
				t.Fatal("fallback usage differs", store.recorded)
			}
			if tc.status == 200 && w.Body.String() != "NEW-CONTENT" || tc.status != 200 && strings.Contains(w.Body.String(), "CONTENT") {
				t.Fatal("unreserved representation escaped", w.Body.String())
			}
		})
	}
}

func TestPublicReadIfRangeFallbackFailure(t *testing.T) {
	for _, fail := range []string{"admission", "provider", "partial full response"} {
		t.Run(fail, func(t *testing.T) {
			calls := 0
			var accounting *uploadTestAccounting
			client := &http.Client{Transport: metadataRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 && fail == "admission" {
					accounting.err = errors.New("unavailable")
				}
				if calls == 2 && fail == "provider" {
					return nil, errors.New("provider unavailable")
				}
				w := httptest.NewRecorder()
				w.Header().Set("ETag", `"current"`)
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.WriteString("partial")
				return w.Result(), nil
			})}
			h, f, _ := newPublicRangeHandler(t, client)
			accounting = f.accounting
			r := httptest.NewRequest(http.MethodGet, "https://upload-test.apps.test/assets/file", nil)
			r.Header.Set("Range", "bytes=4-")
			r.Header.Set("If-Range", `"old"`)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "partial") {
				t.Fatalf("fallback failure exposed partial response: %d %q", w.Code, w.Body.String())
			}
		})
	}
}
