package s3gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (p *gatewayReceiptProvider) SnapshotCopySource(context.Context, string, string) (objectstorage.CopySourceSnapshot, error) {
	return p.copySource, p.sourceErr
}
func (p *gatewayReceiptProvider) CopyTrackedObject(ctx context.Context, _ string, id string, r objectstorage.CopyObjectRequest, source objectstorage.CopySourceSnapshot) (objectstorage.CopyObjectResult, error) {
	return p.copyFn(ctx, id, r, source)
}
func signedCopyTestRequest(t *testing.T) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "http://s3.gregale.dev/assets/destination", nil)
	r.Host = "s3.gregale.dev"
	r.Header.Set("X-Amz-Copy-Source", "/assets/source")
	r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	r.Header.Set("X-Amz-Date", "20260907T120000Z")
	if err := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(t.Context(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return r
}

// adr: 394
func TestGatewayTrackedCopyOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                                                     string
		err                                                      error
		etag                                                     string
		status                                                   int
		phase, receipt                                           string
		dispatchLost, finishLost, sourceLost, metricsLost, quota bool
	}{
		{name: "success", etag: `"copied"`, status: 200, phase: state.ObjectUploadSettled, receipt: "completed"},
		{name: "lost acknowledgment", err: objectstorage.ErrUnavailable, status: 503, phase: state.ObjectUploadDispatched, receipt: "pending"},
		{name: "unproven not found", err: objectstorage.ErrNotFound, status: 503, phase: state.ObjectUploadDispatched, receipt: "pending"},
		{name: "source changed", err: errors.Join(objectstorage.ErrWriteRejected, objectstorage.ErrPreconditionFailed), status: 412, phase: state.ObjectUploadSettled, receipt: "failed"},
		{name: "source removed", err: errors.Join(objectstorage.ErrWriteRejected, objectstorage.ErrNotFound), status: 404, phase: state.ObjectUploadSettled, receipt: "failed"},
		{name: "missing etag", status: 503, phase: state.ObjectUploadDispatched, receipt: "pending"},
		{name: "lost dispatch acknowledgment", dispatchLost: true, status: 503, phase: state.ObjectUploadSettled, receipt: "failed"},
		{name: "settlement unavailable", finishLost: true, etag: `"copied"`, status: 503, phase: state.ObjectUploadDispatched, receipt: "pending"},
		{name: "source probe failed", sourceLost: true, status: 503},
		{name: "source metric failed", metricsLost: true, status: 503},
		{name: "quota rejected", quota: true, status: 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, st, p := newGatewayReceiptHandler(t, func(*http.Request) (*http.Response, error) { t.Fatal("copy used PUT transport"); return nil, nil })
			p.copySource = objectstorage.CopySourceSnapshot{SizeBytes: 5, ETag: `"source"`, Metadata: objectstorage.ObjectMetadata{ContentType: "image/png"}}
			if tc.quota {
				p.copySource.SizeBytes = 101
			}
			if tc.sourceLost {
				p.sourceErr = objectstorage.ErrUnavailable
			}
			if tc.metricsLost {
				h.requestMetrics = &gatewayRequestMetrics{err: errors.New("metrics unavailable")}
			}
			st.dispatchLost, st.finishLost = tc.dispatchLost, tc.finishLost
			var calls atomic.Int32
			p.copyFn = func(_ context.Context, id string, r objectstorage.CopyObjectRequest, source objectstorage.CopySourceSnapshot) (objectstorage.CopyObjectResult, error) {
				calls.Add(1)
				if id == "" || source.ETag != `"source"` || source.SizeBytes != 5 || r.SourceKey != "source" || r.DestinationKey != "destination" {
					t.Error("copy intent not bound")
				}
				return objectstorage.CopyObjectResult{ETag: tc.etag}, tc.err
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, signedCopyTestRequest(t))
			if w.Code != tc.status || strings.Contains(w.Body.String(), "upstream-secret") {
				t.Fatal(w.Code, w.Body.String())
			}
			wantCalls := int32(1)
			if tc.sourceLost || tc.metricsLost || tc.quota || tc.dispatchLost {
				wantCalls = 0
			}
			if calls.Load() != wantCalls {
				t.Fatal("duplicate/invalid dispatch", calls.Load())
			}
			usage, err := st.ObjectUsage(t.Context(), st.bucket.AccountID, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			id := w.Header().Get("X-Gregale-Upload-ID")
			if tc.phase == "" {
				if id != "" || usage.Authorizations != 0 {
					t.Fatal("failed admission persisted", id, usage)
				}
				return
			}
			c, err := st.GetObjectUploadReceipt(t.Context(), st.bucket.AccountID, st.bucket.AppID, "", st.credential.ID, id)
			if err != nil || c.Origin != "gateway_copy" || c.SourceKey != "source" || c.SourceETag != `"source"` || c.WritePhase != tc.phase || c.Status != tc.receipt || usage.Authorizations != 1 {
				t.Fatal(c, usage, err)
			}
		})
	}
}

func TestGatewayTrackedCopyConcurrentOverwrites(t *testing.T) {
	h, st, p := newGatewayReceiptHandler(t, func(*http.Request) (*http.Response, error) { t.Fatal("copy used PUT transport"); return nil, nil })
	p.copySource = objectstorage.CopySourceSnapshot{SizeBytes: 5, ETag: `"source"`}
	entered := make(chan string, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	p.copyFn = func(ctx context.Context, id string, _ objectstorage.CopyObjectRequest, _ objectstorage.CopySourceSnapshot) (objectstorage.CopyObjectResult, error) {
		n := calls.Add(1)
		entered <- id
		select {
		case <-release:
		case <-ctx.Done():
			return objectstorage.CopyObjectResult{}, ctx.Err()
		}
		if n == 1 {
			return objectstorage.CopyObjectResult{}, objectstorage.ErrUnavailable
		}
		return objectstorage.CopyObjectResult{ETag: `"newer"`}, nil
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); h.ServeHTTP(httptest.NewRecorder(), signedCopyTestRequest(t)) }()
	}
	ids := []string{}
	for range 2 {
		select {
		case id := <-entered:
			ids = append(ids, id)
		case <-time.After(5 * time.Second):
			close(release)
			wg.Wait()
			t.Fatal("concurrent copies did not dispatch")
		}
	}
	close(release)
	wg.Wait()
	if ids[0] == ids[1] || calls.Load() != 2 {
		t.Fatal("copies deduplicated/replayed", ids, calls.Load())
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
		t.Fatal("newer copy settled older intent", j, err)
	}
}

func TestGatewayTrackedCopyRequiresDurableStore(t *testing.T) {
	h, st, p := newGatewayReceiptHandler(t, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected PUT transport"); return nil, nil })
	// Retain credential/accounting methods while hiding optional receipt support.
	h.store = struct{ Store }{st}
	p.copyFn = func(context.Context, string, objectstorage.CopyObjectRequest, objectstorage.CopySourceSnapshot) (objectstorage.CopyObjectResult, error) {
		t.Fatal("copy dispatched without durable store")
		return objectstorage.CopyObjectResult{}, nil
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedCopyTestRequest(t))
	usage, err := st.ObjectUsage(t.Context(), st.bucket.AccountID, time.Now())
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("X-Gregale-Upload-ID") != "" || err != nil || usage.Authorizations != 0 {
		t.Fatal(w.Code, usage, err)
	}
}

func TestGatewayCopyRejectsUnknownBodyLength(t *testing.T) {
	h, st, p := newGatewayReceiptHandler(t, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected PUT transport"); return nil, nil })
	p.copyFn = func(context.Context, string, objectstorage.CopyObjectRequest, objectstorage.CopySourceSnapshot) (objectstorage.CopyObjectResult, error) {
		t.Fatal("copy dispatched with an unknown body length")
		return objectstorage.CopyObjectResult{}, nil
	}
	r := signedCopyTestRequest(t)
	r.ContentLength = -1
	r.TransferEncoding = []string{"chunked"}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	usage, err := st.ObjectUsage(t.Context(), st.bucket.AccountID, time.Now())
	if w.Code != http.StatusBadRequest || w.Header().Get("X-Gregale-Upload-ID") != "" || err != nil || usage.Authorizations != 0 {
		t.Fatal(w.Code, usage, err)
	}
}
