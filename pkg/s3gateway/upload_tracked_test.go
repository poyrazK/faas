package s3gateway

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type gatewayReceiptStore struct {
	*state.MemStore
	credential               state.ObjectS3Credential
	bucket                   state.ObjectBucket
	dispatchLost, finishLost bool
}

func (s *gatewayReceiptStore) ResolveObjectS3Credential(_ context.Context, access string) (state.ObjectS3Credential, state.ObjectBucket, error) {
	if access != s.credential.AccessKeyID {
		return state.ObjectS3Credential{}, state.ObjectBucket{}, state.ErrNotFound
	}
	return s.credential, s.bucket, nil
}
func (*gatewayReceiptStore) TouchObjectS3Credential(context.Context, string, time.Time) error {
	return nil
}
func (s *gatewayReceiptStore) DispatchTrackedObjectUpload(ctx context.Context, account, bucket, id string) (state.ObjectUploadCompletion, error) {
	c, err := s.MemStore.DispatchTrackedObjectUpload(ctx, account, bucket, id)
	if err == nil && s.dispatchLost {
		return state.ObjectUploadCompletion{}, errors.New("dispatch response lost")
	}
	return c, err
}
func (s *gatewayReceiptStore) FinishTrackedObjectUpload(ctx context.Context, c state.ObjectUploadCompletion) (state.ObjectUploadCompletion, error) {
	if s.finishLost && c.Status == "completed" {
		return c, errors.New("receipt unavailable")
	}
	return s.MemStore.FinishTrackedObjectUpload(ctx, c)
}

type gatewayReceiptProvider struct {
	mu sync.Mutex
	*gatewayTestProvider
	signErr error
}

func (p *gatewayReceiptProvider) PresignTrackedPut(ctx context.Context, bucket string, r objectstorage.SignRequest, c objectstorage.ObjectWriteConditions, id string) (objectstorage.SignedRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.signErr != nil {
		return objectstorage.SignedRequest{}, p.signErr
	}
	if err := r.Validate(api.MaxObjectSinglePutBytes); err != nil {
		return objectstorage.SignedRequest{}, err
	}
	signed, err := p.PresignConditionalPut(ctx, bucket, r, c)
	signed.Headers["X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey] = id
	return signed, err
}
func (*gatewayReceiptProvider) ConfirmTrackedObject(context.Context, string, string, string, int64) (objectstorage.UploadResult, error) {
	return objectstorage.UploadResult{}, objectstorage.ErrNotFound
}
func newGatewayReceiptHandler(t *testing.T, roundTrip roundTripFunc) (*Handler, *gatewayReceiptStore, *gatewayReceiptProvider) {
	t.Helper()
	st := &gatewayReceiptStore{MemStore: state.NewMemStore()}
	ctx := t.Context()
	acct, err := st.CreateAccount(ctx, "gateway@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "gateway", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	p := &gatewayReceiptProvider{gatewayTestProvider: &gatewayTestProvider{}}
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "test", Region: "us-east-1", Namespace: "test"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"test": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) { return p, nil }})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Name: "assets", Scope: "default", PhysicalName: "physical", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	b.State = "ready"
	st.bucket = b
	st.credential = state.ObjectS3Credential{ID: uuid.NewString(), AccountID: acct.ID, BucketID: b.ID, AccessKeyID: testAccess, SecretSealed: []byte("sealed"), Permission: state.ObjectBucketPermissionReadWrite, Status: state.ObjectS3CredentialStatusActive}
	if err = st.ClaimObjectInventory(ctx, b.ID, "initial"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, b.ID, "initial", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err = st.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: acct.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Registry: registry, Store: st, Host: "s3.gregale.dev", Region: "us-east-1", SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1, OpenSecret: func([]byte) (string, error) { return testSecret, nil }, HTTPClient: &http.Client{Transport: roundTrip}, Now: func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return h, st, p
}

// adr: 393
func TestGatewayTrackedPutOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		status                                     int
		etag                                       string
		transport, metrics, sign, dispatch, finish bool
		wantStatus                                 int
		wantPhase, wantReceipt                     string
	}{
		{name: "acknowledged", status: 200, etag: `"etag"`, wantStatus: 200, wantPhase: state.ObjectUploadSettled, wantReceipt: "completed"},
		{name: "lost response", transport: true, wantStatus: 503, wantPhase: state.ObjectUploadDispatched, wantReceipt: "pending"},
		{name: "server failure", status: 503, wantStatus: 503, wantPhase: state.ObjectUploadDispatched, wantReceipt: "pending"},
		{name: "timeout", status: 408, wantStatus: 503, wantPhase: state.ObjectUploadDispatched, wantReceipt: "pending"},
		{name: "missing etag", status: 200, wantStatus: 503, wantPhase: state.ObjectUploadDispatched, wantReceipt: "pending"},
		{name: "invalid etag", status: 200, etag: strings.Repeat("x", api.MaxObjectWriteETagBytes+1), wantStatus: 503, wantPhase: state.ObjectUploadDispatched, wantReceipt: "pending"},
		{name: "condition rejected", status: 412, wantStatus: 412, wantPhase: state.ObjectUploadSettled, wantReceipt: "failed"},
		{name: "metric failure", metrics: true, wantStatus: 503, wantPhase: state.ObjectUploadSettled, wantReceipt: "failed"},
		{name: "sign failure", sign: true, wantStatus: 503, wantPhase: state.ObjectUploadSettled, wantReceipt: "failed"},
		{name: "dispatch response lost", dispatch: true, wantStatus: 503, wantPhase: state.ObjectUploadSettled, wantReceipt: "failed"},
		{name: "receipt unavailable", finish: true, status: 200, etag: `"etag"`, wantStatus: 503, wantPhase: state.ObjectUploadDispatched, wantReceipt: "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h, st, p := newGatewayReceiptHandler(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.GetBody != nil || r.Header.Get("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey) == "" {
					t.Error("unbound/replayable request")
				}
				data, err := io.ReadAll(r.Body)
				if err != nil || string(data) != "data" {
					t.Error(string(data), err)
				}
				if tc.transport {
					return nil, errors.New("lost")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Etag": []string{tc.etag}}, Body: io.NopCloser(strings.NewReader("private provider response"))}, nil
			})
			st.dispatchLost = tc.dispatch
			st.finishLost = tc.finish
			if tc.metrics {
				h.requestMetrics = &gatewayRequestMetrics{err: errors.New("metrics unavailable")}
			}
			if tc.sign {
				p.signErr = objectstorage.ErrUnavailable
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, signedGatewayRequest(t, "PUT", "http://s3.gregale.dev/assets/key", []byte("data"), "UNSIGNED-PAYLOAD"))
			if rr.Code != tc.wantStatus || strings.Contains(rr.Body.String(), "private provider") {
				t.Fatal(rr.Code, rr.Body.String())
			}
			id := rr.Header().Get("X-Gregale-Upload-ID")
			c, err := st.GetObjectUploadReceipt(t.Context(), st.bucket.AccountID, st.bucket.AppID, "", st.credential.ID, id)
			if err != nil || c.WritePhase != tc.wantPhase || c.Status != tc.wantReceipt || c.Origin != "gateway" {
				t.Fatal(c, err)
			}
			wantCalls := 1
			if tc.metrics || tc.sign || tc.dispatch {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatal("provider attempts", calls)
			}
			usage, err := st.ObjectUsage(t.Context(), st.bucket.AccountID, time.Now())
			if err != nil || usage.Authorizations != 1 || usage.Buckets[0].GrantedBytes != 4 {
				t.Fatal(usage, err)
			}
		})
	}
}

// adr: 393
func TestGatewayTrackedConcurrentOverwrites(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	var countMu sync.Mutex
	calls := 0
	h, st, _ := newGatewayReceiptHandler(t, func(r *http.Request) (*http.Response, error) {
		countMu.Lock()
		calls++
		n := calls
		countMu.Unlock()
		entered <- r.Header.Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		if n == 1 {
			return nil, errors.New("lost accepted response")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Etag": []string{`"newer"`}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), signedGatewayRequest(t, "PUT", "http://s3.gregale.dev/assets/key", []byte("data"), "UNSIGNED-PAYLOAD"))
		}()
	}
	ids := []string{}
	for range 2 {
		select {
		case id := <-entered:
			ids = append(ids, id)
		case <-time.After(5 * time.Second):
			close(release)
			wg.Wait()
			t.Fatal("concurrent PUT did not dispatch")
		}
	}
	close(release)
	wg.Wait()
	if ids[0] == ids[1] {
		t.Fatal("distinct S3 writes deduplicated")
	}
	statuses := map[string]int{}
	for _, id := range ids {
		c, err := st.GetObjectUploadReceipt(t.Context(), st.bucket.AccountID, st.bucket.AppID, "", st.credential.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		statuses[c.Status]++
	}
	if statuses["pending"] != 1 || statuses["completed"] != 1 {
		t.Fatal(statuses)
	}
	j, err := st.RequestObjectCapacityReconciliation(t.Context(), st.bucket.AccountID, st.bucket.AppID, st.bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(t.Context(), j.ID, "scan")
	if err != nil || j.State != "waiting" || j.PendingWrites != 1 {
		t.Fatal("newer acknowledgment settled older write", j, err)
	}
}
