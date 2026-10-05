package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type trackedRouteTestProvider struct {
	*uploadTestProvider
	outcome          error
	receipt          string
	started, release chan struct{}
}

func (p *trackedRouteTestProvider) WriteTrackedObject(ctx context.Context, bucket, key, receipt string, body io.Reader, size int64, metadata ObjectMetadata) (UploadResult, error) {
	if p.started != nil {
		close(p.started)
		<-p.release
	}
	result, err := p.WriteObject(ctx, bucket, key, body, size, metadata)
	if err != nil {
		return result, err
	}
	p.receipt = receipt
	return result, p.outcome
}
func (p *trackedRouteTestProvider) ConfirmTrackedObject(_ context.Context, _, key, receipt string, size int64) (UploadResult, error) {
	if p.key != key || p.receipt != receipt || int64(len(p.body)) != size {
		return UploadResult{}, ErrConflict
	}
	return UploadResult{ETag: "etag-test"}, nil
}
func newTrackedUploadFixture(t *testing.T) (uploadFixture, *trackedRouteTestProvider) {
	t.Helper()
	f := newUploadFixture(t)
	ctx := context.Background()
	if err := f.store.ClaimObjectInventory(ctx, f.route.BucketID, "upload-inventory"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.FinishObjectInventory(ctx, f.route.BucketID, "upload-inventory", 0, 0); err != nil {
		t.Fatal(err)
	}
	backend := f.registry.backends["test"]
	provider := &trackedRouteTestProvider{uploadTestProvider: f.provider}
	backend.Provider = provider
	f.registry.backends["test"] = backend
	if err := f.store.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: f.account.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	handler, err := NewUploadHandler(UploadConfig{Store: f.store, Routes: f.store, Buckets: f.store, Authenticator: f.store, Registry: f.registry, Accounting: f.store, AppsDomain: "apps.test", Next: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })})
	if err != nil {
		t.Fatal(err)
	}
	f.handler = handler
	return f, provider
}
func TestTrackedUploadConcurrentRequests(t *testing.T) {
	f, p := newTrackedUploadFixture(t)
	p.started = make(chan struct{})
	p.release = make(chan struct{})
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		first <- f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "once")
	}()
	select {
	case <-p.started:
	case res := <-first:
		t.Fatalf("request ended before dispatch: %d %s", res.Code, res.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("provider dispatch did not start")
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "once")
			if res.Code != http.StatusConflict {
				t.Error(res.Code, res.Body.String())
			}
		}()
	}
	wg.Wait()
	close(p.release)
	if res := <-first; res.Code != http.StatusCreated {
		t.Fatal(res.Code, res.Body.String())
	}
	usage, err := f.store.ObjectUsage(context.Background(), f.account.ID, time.Now())
	if err != nil || usage.Authorizations != 1 || usage.Buckets[0].GrantedBytes != 6 || usage.Buckets[0].GrantedKeys != 1 || p.writes != 1 {
		t.Fatal("duplicate dispatch/admission", usage, p.writes, err)
	}
	if res := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "once"); res.Code != http.StatusCreated {
		t.Fatal(res.Code, res.Body.String())
	}
}
func TestTrackedUploadUncertainOutcomeAndPrivateReceipt(t *testing.T) {
	f, p := newTrackedUploadFixture(t)
	p.outcome = ErrUnavailable
	res := f.request(http.MethodPost, "/uploads/avatar", "avatar", "image/png")
	id := res.Header().Get("X-Gregale-Upload-ID")
	if res.Code != http.StatusBadGateway || id == "" {
		t.Fatal(res.Code, res.Body.String(), id)
	}
	receipt := f.request(http.MethodGet, "/uploads/avatar/receipts/"+id, "", "")
	if receipt.Code != http.StatusOK || !strings.Contains(receipt.Body.String(), `"status":"pending"`) {
		t.Fatal(receipt.Code, receipt.Body.String())
	}
	for _, private := range []string{"write_phase", "recovery_token", "physical_name", "account_id"} {
		if strings.Contains(receipt.Body.String(), private) {
			t.Fatal("private data", private)
		}
	}
	// Receipt reads survive the feature flag being disabled.
	f.handler.(*uploadHandler).enabled = func() bool { return false }
	if receipt = f.request(http.MethodGet, "/uploads/avatar/receipts/"+id, "", ""); receipt.Code != http.StatusOK {
		t.Fatal(receipt.Code)
	}
	otherToken, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateAPIKey(context.Background(), f.account.ID, hash, "other", []string{api.ScopeStorageWrite}); err != nil {
		t.Fatal(err)
	}
	f.token = otherToken
	if receipt = f.request(http.MethodGet, "/uploads/avatar/receipts/"+id, "", ""); receipt.Code != http.StatusNotFound {
		t.Fatal("other principal read receipt", receipt.Code)
	}
}
func TestTrackedUploadRecoveryResultReplaysAndReclaims(t *testing.T) {
	f, p := newTrackedUploadFixture(t)
	p.outcome = ErrUnavailable
	first := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "recover")
	if first.Code != http.StatusBadGateway {
		t.Fatal(first.Code)
	}
	c, err := f.store.GetObjectUploadIntent(context.Background(), f.route.ID, f.key.ID, "recover")
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.ConfirmTrackedObject(context.Background(), p.bucket, c.Key, c.ID, c.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	c.Status = "completed"
	c.ETag = result.ETag
	if _, err = f.store.FinishTrackedObjectUpload(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	replay := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "recover")
	if replay.Code != http.StatusCreated || p.writes != 1 {
		t.Fatal(replay.Code, p.writes, replay.Body.String())
	}
	var body map[string]any
	if err = json.Unmarshal(replay.Body.Bytes(), &body); err != nil || body["id"] != c.ID {
		t.Fatal(body, err)
	}
	// Provider deletion and a complete fenced inventory make the reservation reusable.
	p.body = nil
	ctx := context.Background()
	j, err := f.store.RequestObjectCapacityReconciliation(ctx, c.AccountID, c.AppID, c.BucketID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = f.store.ClaimObjectCapacityReconciliation(ctx, j.ID, "inventory")
	if err != nil || j.State != "scanning" {
		t.Fatal(j, err)
	}
	j, err = f.store.FinishObjectCapacityReconciliation(ctx, j.ID, "inventory", 0, 0)
	if err != nil || j.ReclaimedBytes != 6 {
		t.Fatal(j, err)
	}
	p.outcome = nil
	if res := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "new"); res.Code != http.StatusCreated {
		t.Fatal(res.Code, res.Body.String())
	}
}
func TestTrackedUploadRejectsReadOnlyKeyAndDefinitiveFailure(t *testing.T) {
	f, p := newTrackedUploadFixture(t)
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateAPIKey(context.Background(), f.account.ID, hash, "reader", []string{api.ScopeStorageRead}); err != nil {
		t.Fatal(err)
	}
	oldToken := f.token
	f.token = token
	if res := f.request(http.MethodPost, "/uploads/avatar", "avatar", "image/png"); res.Code != http.StatusForbidden || p.writes != 0 {
		t.Fatal(res.Code, p.writes)
	}
	f.token = oldToken
	p.outcome = ErrWriteRejected
	if res := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "reject"); res.Code != http.StatusBadGateway {
		t.Fatal(res.Code)
	}
	c, err := f.store.GetObjectUploadIntent(context.Background(), f.route.ID, f.key.ID, "reject")
	if err != nil || c.Status != "failed" || c.WritePhase != state.ObjectUploadSettled {
		t.Fatal(c, err)
	}
	c.Status = "completed"
	c.ETag = "other"
	c.ErrorCode = ""
	if _, err = f.store.FinishTrackedObjectUpload(context.Background(), c); !errors.Is(err, state.ErrConflict) {
		t.Fatal("rejection rewritten", err)
	}
}

type failingTrackedReceiptStore struct {
	state.ObjectUploadRouteStore
	state.ObjectTrackedUploadStore
}

func (s *failingTrackedReceiptStore) FinishTrackedObjectUpload(context.Context, state.ObjectUploadCompletion) (state.ObjectUploadCompletion, error) {
	return state.ObjectUploadCompletion{}, errors.New("database unavailable")
}
func TestTrackedUploadReceiptPersistenceFailureStaysRecoverable(t *testing.T) {
	f, p := newTrackedUploadFixture(t)
	f.handler.(*uploadHandler).routes = &failingTrackedReceiptStore{ObjectUploadRouteStore: f.store, ObjectTrackedUploadStore: f.store}
	res := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "database-failure")
	if res.Code != http.StatusServiceUnavailable || p.writes != 1 {
		t.Fatal(res.Code, p.writes, res.Body.String())
	}
	c, err := f.store.GetObjectUploadIntent(context.Background(), f.route.ID, f.key.ID, "database-failure")
	if err != nil || c.Status != "pending" {
		t.Fatal("uncommitted success reported", c, err)
	}
	if _, err = p.ConfirmTrackedObject(context.Background(), p.bucket, c.Key, c.ID, c.Bytes); err != nil {
		t.Fatal("receipt missing from accepted object", err)
	}
	replay := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "database-failure")
	if replay.Code != http.StatusConflict || p.writes != 1 {
		t.Fatal("receipt failure caused reupload", replay.Code, p.writes)
	}
}
