package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectCustomerRequestAccountingMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	objectCustomerRequestAccounting(t, e.s, e.store, e.acct, e.key)
}

// Every native page/attempt consumes the shared account budget, before I/O.
// Status, accepted recovery, and rejected selections have different semantics.
func objectCustomerRequestAccounting(t *testing.T, s *server, st state.Store, acct state.Account, bearer string) {
	t.Helper()
	var calls atomic.Int64
	var fail atomic.Bool
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `<Error><Code>ServiceUnavailable</Code></Error>`)
			return
		}
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case q.Has("list-type"):
			_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
		case q.Has("uploadId"):
			part, next := 1, "<NextPartNumberMarker>1</NextPartNumberMarker>"
			if q.Get("part-number-marker") == "1" {
				part, next = 2, ""
			}
			_, _ = fmt.Fprintf(w, `<ListPartsResult><IsTruncated>%t</IsTruncated>%s<Part><PartNumber>%d</PartNumber><ETag>&quot;part-%d&quot;</ETag><Size>5</Size></Part></ListPartsResult>`, part == 1, next, part, part)
		case q.Has("versioning"):
			_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
		case q.Has("encryption"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>ServerSideEncryptionConfigurationNotFoundError</Code></Error>`)
		case q.Has("object-lock"):
			_, _ = io.WriteString(w, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
		case q.Has("retention"):
			_, _ = io.WriteString(w, `<Retention><Mode>COMPLIANCE</Mode><RetainUntilDate>2099-01-01T00:00:00Z</RetainUntilDate></Retention>`)
		case q.Has("legal-hold"):
			_, _ = io.WriteString(w, `<LegalHold><Status>OFF</Status></LegalHold>`)
		case q.Has("tagging"):
			_, _ = io.WriteString(w, `<Tagging><TagSet/></Tagging>`)
		default:
			t.Error("unexpected native request", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(native.Close)
	_, b, _, policy := seedEncryptionJournalStorage(t, s, st, acct, native.URL)
	s.WithObjectStorage(objectLockTestRegistry(t, native.URL, policy, objectstorage.ObjectLockConfig{Enabled: true}))
	since := time.Now().UTC().Add(-time.Minute)
	policy.AccountingMode, policy.GatewayMeteringSince, policy.MaxMonthlyCostMillicents = api.ObjectStorageGatewaySafetyV1, &since, 0
	s.objectStorage.Accounting = policy
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	uploads := st.(state.ObjectMultipartUploadStore)
	u, err := uploads.ReserveObjectMultipartUpload(t.Context(), state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: acct.ID, AppID: b.AppID, BucketID: b.ID, Key: "key", SizeBytes: 10, PartSizeBytes: 5, PartCount: 2, ExpiresAt: time.Now().Add(time.Hour)}, api.MaxActiveMultipartUploadsPerBucket)
	if err != nil {
		t.Fatal(err)
	}
	u, err = uploads.ClaimObjectMultipartUpload(t.Context(), acct.ID, b.AppID, b.ID, u.ID, uuid.NewString(), state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = uploads.ActivateObjectMultipartUpload(t.Context(), u.ID, u.LeaseToken, "native-upload"); err != nil {
		t.Fatal(err)
	}
	refs, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(t.Context(), acct.ID, b.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "native-version"}})
	if err != nil || len(refs) != 1 {
		t.Fatal(refs, err)
	}
	base := "/v1/apps/encrypted-journal/buckets/" + b.ID
	session := base + "/multipart-uploads/" + u.ID
	parts := session + "/parts"
	h := s.handler()
	do := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	usage := func() int64 {
		t.Helper()
		snapshot, e := st.(state.ObjectStorageAccountingStore).ObjectUsage(t.Context(), acct.ID, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		return state.SummarizeObjectUsage(snapshot, s.objectStorage.Accounting, time.Now()).RequestCount
	}
	assertResponse := func(t *testing.T, path string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := do(path, bearer)
		if w.Code != status {
			t.Fatalf("%s: %d want %d: %s", path, w.Code, status, w.Body.String())
		}
		return w
	}
	for _, tc := range []struct {
		name, path string
		attempts   int64
	}{
		{"objects", base + "/objects?limit=1", 1},
		{"parts", parts, 1},
		{"versioning", base + "/versioning", 1},
		{"encryption", base + "/encryption", 1},
		{"object lock", base + "/object-lock", 1},
		{"retention", base + "/objects/protection/retention?key=key&version_id=" + refs[0].ID, 3},
		{"legal hold", base + "/objects/protection/legal-hold?key=key&version_id=" + refs[0].ID, 3},
		{"tags", base + "/objects/tags?key=key", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, dispatched := usage(), calls.Load()
			s.objectStorage.Accounting.MaxMonthlyRequests = before + tc.attempts
			assertResponse(t, tc.path, http.StatusOK)
			if usage() != before+tc.attempts || calls.Load() != dispatched+tc.attempts {
				t.Fatal("attempt not recorded exactly once", usage()-before, calls.Load()-dispatched)
			}
			deniedStatus := http.StatusPaymentRequired
			if tc.name == "object lock" {
				// Pending control intent can be inspected from durable state when
				// the live probe is denied. It must not contact the provider.
				deniedStatus = http.StatusOK
			}
			assertResponse(t, tc.path, deniedStatus)
			if usage() != before+tc.attempts || calls.Load() != dispatched+tc.attempts {
				t.Fatal("budget denial reached provider or changed usage")
			}
		})
	}
	t.Run("multi-probe boundary", func(t *testing.T) {
		before, dispatched := usage(), calls.Load()
		s.objectStorage.Accounting.MaxMonthlyRequests = before + 2
		assertResponse(t, base+"/objects/protection/retention?key=key&version_id="+refs[0].ID, http.StatusPaymentRequired)
		if usage() != before+2 || calls.Load() != dispatched+2 {
			t.Fatal("protection probes crossed the budget boundary", usage()-before, calls.Load()-dispatched)
		}
	})
	t.Run("failed object listing attempt", func(t *testing.T) {
		before, dispatched := usage(), calls.Load()
		s.objectStorage.Accounting.MaxMonthlyRequests = before + 1
		fail.Store(true)
		assertResponse(t, base+"/objects", http.StatusServiceUnavailable)
		fail.Store(false)
		assertResponse(t, base+"/objects", http.StatusPaymentRequired)
		if usage() != before+1 || calls.Load() != dispatched+1 {
			t.Fatal("object listing retried without admission", usage()-before, calls.Load()-dispatched)
		}
	})
	t.Run("each page and failed attempt", func(t *testing.T) {
		before, dispatched := usage(), calls.Load()
		s.objectStorage.Accounting.MaxMonthlyRequests = before + 3
		first := assertResponse(t, parts+"?limit=1", http.StatusOK)
		var page api.ObjectMultipartPartList
		if json.Unmarshal(first.Body.Bytes(), &page) != nil || page.NextPartNumberMarker != 1 {
			t.Fatal(first.Body.String())
		}
		assertResponse(t, parts+"?limit=1&part_number_marker=1", http.StatusOK)
		fail.Store(true)
		assertResponse(t, parts, http.StatusServiceUnavailable)
		fail.Store(false)
		assertResponse(t, parts, http.StatusPaymentRequired)
		if usage() != before+3 || calls.Load() != dispatched+3 {
			t.Fatal("pages/failure accounting", usage()-before, calls.Load()-dispatched)
		}
		// Status/list are ledger reads and remain available at the ceiling.
		assertResponse(t, session, http.StatusOK)
		assertResponse(t, base+"/multipart-uploads", http.StatusOK)
		if usage() != before+3 || calls.Load() != dispatched+3 {
			t.Fatal("metadata consumed native request usage")
		}
	})
	t.Run("rejected selections", func(t *testing.T) {
		before, dispatched := usage(), calls.Load()
		s.objectStorage.Accounting.MaxMonthlyRequests = before + 10
		for _, query := range []string{"?limit=0", "?limit=1001", "?part_number_marker=-1", "?part_number_marker=10001", "?limit=abc", "?limit=", "?limit=1&limit=2", "?unexpected=1", "?limit=1;part_number_marker=1"} {
			assertResponse(t, parts+query, http.StatusBadRequest)
		}
		r := httptest.NewRequest(http.MethodGet, parts, strings.NewReader("unexpected body"))
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatal("part list accepted a body", w.Code, w.Body.String())
		}
		assertResponse(t, base+"/multipart-uploads/"+uuid.NewString()+"/parts", http.StatusNotFound)
		plain, hash, e := api.GenerateAPIKey()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = st.CreateAPIKey(t.Context(), acct.ID, hash, "ungranted", []string{api.ScopeStorageWrite}); e != nil {
			t.Fatal(e)
		}
		if w := do(parts, plain); w.Code != http.StatusForbidden {
			t.Fatal(w.Code, w.Body.String())
		}
		other, e := st.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanPro)
		if e != nil {
			t.Fatal(e)
		}
		plain, hash, e = api.GenerateAPIKey()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = st.CreateAPIKey(t.Context(), other.ID, hash, "foreign", api.ScopesAdminOnly); e != nil {
			t.Fatal(e)
		}
		if w := do(parts, plain); w.Code != http.StatusNotFound {
			t.Fatal(w.Code, w.Body.String())
		}
		if usage() != before || calls.Load() != dispatched {
			t.Fatal("rejected selection charged or dispatched")
		}
	})
	t.Run("unqualified coverage", func(t *testing.T) {
		before, dispatched := usage(), calls.Load()
		future := time.Now().UTC().Add(time.Minute)
		s.objectStorage.Accounting.GatewayMeteringSince = &future
		w := assertResponse(t, parts, http.StatusServiceUnavailable)
		if !strings.Contains(w.Body.String(), "object_storage_usage_stale") || usage() != before || calls.Load() != dispatched {
			t.Fatal("unqualified coverage reached native provider", w.Body.String())
		}
		s.objectStorage.Accounting.GatewayMeteringSince = &since
	})
	t.Run("concurrent final request", func(t *testing.T) {
		before, dispatched := usage(), calls.Load()
		s.objectStorage.Accounting.MaxMonthlyRequests = before + 1
		var winners atomic.Int32
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				path := parts
				if i%2 == 0 {
					path = base + "/versioning"
				}
				w := do(path, bearer)
				if w.Code == http.StatusOK {
					winners.Add(1)
				} else if w.Code != http.StatusPaymentRequired {
					t.Error(w.Code, w.Body.String())
				}
			}()
		}
		wg.Wait()
		if winners.Load() != 1 || usage() != before+1 || calls.Load() != dispatched+1 {
			t.Fatal("concurrent budget overspent", winners.Load(), usage()-before, calls.Load()-dispatched)
		}
	})
}

// A store exposing metadata alone cannot silently disable gateway admission.
func TestObjectCustomerRequestAccountingMissingMeter(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithObjectStorage(objectRegistry(t, &fakeObjectProvider{}, &fakeObjectProvider{}, "external"))
	e.s.objectStorage.Accounting.AccountingMode = api.ObjectStorageGatewaySafetyV1
	e.s.store = struct{ state.Store }{e.store}
	if err := e.s.customerObjectRequestRecorder(state.ObjectBucket{ID: uuid.NewString()})(context.Background()); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatal("missing request meter did not fail closed", err)
	}
	e.s.objectStorage.Accounting.AccountingMode = ""
	if err := e.s.customerObjectRequestRecorder(state.ObjectBucket{ID: uuid.NewString()})(context.Background()); !errors.Is(err, objectstorage.ErrConfiguration) {
		t.Fatal("legacy customer request escaped without a meter", err)
	}
}
