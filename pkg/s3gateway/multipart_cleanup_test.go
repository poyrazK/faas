package s3gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGatewayAbortWaitsForInFlightPart(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	h, store, p := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, func(r *http.Request) (*http.Response, error) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return nil, err
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 31*time.Minute {
			t.Error("unbounded transfer context")
		}
		close(started)
		<-release
		return &http.Response{StatusCode: 200, Header: http.Header{"Etag": {`"etag-1"`}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	sessions := newGatewayMultipartStore()
	h.multipartStore = sessions
	id := uuid.NewString()
	sessions.uploads[id] = state.ObjectMultipartUpload{ID: id, AccountID: store.bucket.AccountID, AppID: store.bucket.AppID, BucketID: store.bucket.ID, Key: "key", ProviderUploadID: "provider", State: state.ObjectMultipartActive, ExpiresAt: h.now().Add(time.Hour)}
	part := httptest.NewRecorder()
	done := make(chan struct{})
	r := signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key?uploadId="+id+"&partNumber=1", []byte("data"), "UNSIGNED-PAYLOAD")
	go func() { defer close(done); h.ServeHTTP(part, r) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("part did not reach provider")
	}
	complete := httptest.NewRecorder()
	h.ServeHTTP(complete, signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets/key?uploadId="+id, []byte(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"etag-1"</ETag></Part></CompleteMultipartUpload>`), "UNSIGNED-PAYLOAD"))
	if complete.Code != 409 || len(p.completedUploads) != 0 {
		t.Errorf("in-flight completion: %d %s", complete.Code, complete.Body.String())
	}
	abort := httptest.NewRecorder()
	h.ServeHTTP(abort, signedGatewayRequest(t, http.MethodDelete, "https://s3.gregale.dev/assets/key?uploadId="+id, nil, "UNSIGNED-PAYLOAD"))
	u, err := sessions.GetObjectMultipartUpload(t.Context(), store.bucket.AccountID, store.bucket.AppID, store.bucket.ID, id)
	if abort.Code != 204 || err != nil || u.State != state.ObjectMultipartAborting || u.LeaseToken != "" {
		t.Errorf("early release: %d %+v %v", abort.Code, u, err)
	}
	recreate := httptest.NewRecorder()
	h.ServeHTTP(recreate, signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets/key?uploads=", nil, "UNSIGNED-PAYLOAD"))
	if recreate.Code != 409 {
		t.Errorf("create reused an aborting upload: %d", recreate.Code)
	}
	late := httptest.NewRecorder()
	h.ServeHTTP(late, signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key?uploadId="+id+"&partNumber=2", []byte("data"), "UNSIGNED-PAYLOAD"))
	if late.Code != 409 {
		t.Errorf("write after abort: %d", late.Code)
	}
	close(release)
	<-done
	if part.Code != 200 {
		t.Fatalf("part result: %d %s", part.Code, part.Body.String())
	}
	// Advance the durable cleanup cooldown after the part has settled.
	sessions.mu.Lock()
	pendingCleanup := sessions.uploads[id]
	pendingCleanup.RetryAt = time.Now().Add(-time.Second)
	sessions.uploads[id] = pendingCleanup
	sessions.mu.Unlock()
	h.enabled = func() bool { return false }
	abort = httptest.NewRecorder()
	h.ServeHTTP(abort, signedGatewayRequest(t, http.MethodDelete, "https://s3.gregale.dev/assets/key?uploadId="+id, nil, "UNSIGNED-PAYLOAD"))
	u, err = sessions.GetObjectMultipartUpload(t.Context(), store.bucket.AccountID, store.bucket.AppID, store.bucket.ID, id)
	if abort.Code != 204 || err != nil || u.State != state.ObjectMultipartAborted || len(p.abortedUploads) != 2 {
		t.Fatalf("cleanup: %d %+v %v", abort.Code, u, err)
	}
}

func TestGatewayAbortRequiresProviderVerification(t *testing.T) {
	for _, tc := range []struct {
		name       string
		keepParts  bool
		listErr    error
		wantStatus int
	}{
		{name: "late parts", keepParts: true, wantStatus: 204},
		{name: "listing unavailable", listErr: objectstorage.ErrUnavailable, wantStatus: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, store, p := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, nil)
			sessions := newGatewayMultipartStore()
			h.multipartStore = sessions
			id := uuid.NewString()
			sessions.uploads[id] = state.ObjectMultipartUpload{ID: id, AccountID: store.bucket.AccountID, AppID: store.bucket.AppID, BucketID: store.bucket.ID, Key: "key", ProviderUploadID: "provider", State: state.ObjectMultipartActive}
			p.keepPartsOnAbort, p.multipartListErr = tc.keepParts, tc.listErr
			p.multipart = map[string]map[int32]objectstorage.MultipartPart{"provider": {1: {PartNumber: 1, ETag: "etag", SizeBytes: 1}}}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, signedGatewayRequest(t, http.MethodDelete, "https://s3.gregale.dev/assets/key?uploadId="+id, nil, "UNSIGNED-PAYLOAD"))
			u, err := sessions.GetObjectMultipartUpload(t.Context(), store.bucket.AccountID, store.bucket.AppID, store.bucket.ID, id)
			if w.Code != tc.wantStatus || err != nil || u.State != state.ObjectMultipartAborting || u.LeaseToken != "" {
				t.Fatalf("%d %+v %v", w.Code, u, err)
			}
		})
	}
}

func TestGatewayFailedPartRetainsTransfer(t *testing.T) {
	h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, func(*http.Request) (*http.Response, error) { return nil, errors.New("uncertain provider response") })
	sessions := newGatewayMultipartStore()
	h.multipartStore = sessions
	id := uuid.NewString()
	sessions.uploads[id] = state.ObjectMultipartUpload{ID: id, AccountID: store.bucket.AccountID, AppID: store.bucket.AppID, BucketID: store.bucket.ID, Key: "key", ProviderUploadID: "provider", State: state.ObjectMultipartActive, ExpiresAt: h.now().Add(time.Hour)}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key?uploadId="+id+"&partNumber=1", []byte("data"), "UNSIGNED-PAYLOAD"))
	if w.Code != 503 || sessions.pending[id][1] == "" {
		t.Fatalf("uncertain transfer discarded: %d", w.Code)
	}
}

func TestGatewayPartFailureBeforeWriteSettlesTransfer(t *testing.T) {
	h, store, p := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("signing failure reached provider transport")
		return nil, nil
	})
	sessions := newGatewayMultipartStore()
	h.multipartStore = sessions
	id := uuid.NewString()
	sessions.uploads[id] = state.ObjectMultipartUpload{ID: id, AccountID: store.bucket.AccountID, AppID: store.bucket.AppID, BucketID: store.bucket.ID, Key: "key", ProviderUploadID: "provider", State: state.ObjectMultipartActive, ExpiresAt: h.now().Add(time.Hour)}
	p.multipartSignErr = objectstorage.ErrUnavailable
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key?uploadId="+id+"&partNumber=1", []byte("data"), "UNSIGNED-PAYLOAD"))
	if w.Code != 503 || sessions.pending[id][1] != "" || len(sessions.partGrants) != 1 {
		t.Fatalf("pre-write failure retained transfer: %d pending=%v", w.Code, sessions.pending)
	}
}
