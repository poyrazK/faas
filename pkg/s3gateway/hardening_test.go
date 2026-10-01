package s3gateway

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (p *gatewayTestProvider) PresignConditionalPut(ctx context.Context, bucket string, r objectstorage.SignRequest, c objectstorage.ObjectWriteConditions) (objectstorage.SignedRequest, error) {
	p.conditions = append(p.conditions, c)
	out, e := p.Presign(ctx, bucket, r)
	if c.IfMatch != "" {
		out.Headers["If-Match"] = c.IfMatch
	}
	if c.IfNoneMatch != "" {
		out.Headers["If-None-Match"] = c.IfNoneMatch
	}
	return out, e
}
func (p *gatewayTestProvider) ListObjectsV2(_ context.Context, _ string, r objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
	p.listRequests = append(p.listRequests, r)
	return p.objects, nil
}
func (s *gatewayMultipartStore) AdmitObjectMultipartPart(_ context.Context, _, _, _ string, _ int32, size, _ int64, _ api.ObjectStoragePolicy) error {
	s.partGrants = append(s.partGrants, size)
	return s.capacityError
}
func (s *gatewayMultipartStore) AdmitObjectMultipartCompletion(_ context.Context, _, _, _, _ string, size int64, _ api.ObjectStoragePolicy) error {
	s.completionGrants = append(s.completionGrants, size)
	return s.capacityError
}
func (s *gatewayMultipartStore) ListObjectS3MultipartUploads(_ context.Context, account, app, bucket, prefix, keyMarker, uploadMarker string, limit int32) ([]state.ObjectMultipartUpload, error) {
	rows := []state.ObjectMultipartUpload{}
	for _, u := range s.uploads {
		if u.AccountID == account && u.AppID == app && u.BucketID == bucket && u.PartCount == 0 && u.State == state.ObjectMultipartActive && strings.HasPrefix(u.Key, prefix) && (keyMarker == "" || u.Key > keyMarker || u.Key == keyMarker && uploadMarker != "" && u.ID > uploadMarker) {
			rows = append(rows, u)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Key < rows[j].Key || rows[i].Key == rows[j].Key && rows[i].ID < rows[j].ID
	})
	if len(rows) > int(limit) {
		rows = rows[:limit]
	}
	return rows, nil
}
func TestGatewayConditionalPutPreserved(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusPreconditionFailed, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h, _, p := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("If-None-Match") != "*" {
					t.Fatal("lost write condition")
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
			})
			r := signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key", []byte("data"), "UNSIGNED-PAYLOAD")
			r.Header.Set("If-None-Match", "*")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != status || len(p.conditions) != 1 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestGatewayRejectsConditionalCompletion(t *testing.T) {
	h, _, p := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected upstream call"); return nil, nil })
	r := signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets/key?uploadId=unknown", nil, "UNSIGNED-PAYLOAD")
	r.Header.Set("If-Match", "\"etag\"")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotImplemented || len(p.presignRequests) != 0 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestGatewayListObjectsV2Parameters(t *testing.T) {
	h, _, p := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected upstream call"); return nil, nil })
	p.objects = objectstorage.ObjectPage{Items: []objectstorage.Object{{Key: "folder/hello 世界.txt", ETag: "\"etag\"", Size: 4}}, CommonPrefixes: []string{"folder/a b/"}, NextCursor: "opaque+/token"}
	r := signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets?list-type=2&encoding-type=url&prefix=folder%2F&delimiter=%2F&start-after=folder%2Fa&continuation-token=previous", nil, "UNSIGNED-PAYLOAD")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result listBucketResult
	if e := xml.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if w.Code != 200 || result.Contents[0].Key != "folder%2Fhello%20%E4%B8%96%E7%95%8C.txt" || result.Contents[0].ETag != "\"etag\"" || result.StartAfter != "folder%2Fa" || result.ContinuationToken != "previous" || result.NextContinuationToken != "opaque+/token" || p.listRequests[0].StartAfter != "folder/a" {
		t.Fatalf("result=%+v requests=%+v", result, p.listRequests)
	}
	r = signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets?list-type=2&max-keys=0", nil, "UNSIGNED-PAYLOAD")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<KeyCount>0</KeyCount>") || len(p.listRequests) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	r = signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets?list-type=2&fetch-owner=true", nil, "UNSIGNED-PAYLOAD")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 501 {
		t.Fatal(w.Code)
	}
}
func TestGatewayCORSAndDisabledCleanup(t *testing.T) {
	h, store, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected upstream call"); return nil, nil })
	r := httptest.NewRequest(http.MethodOptions, "https://s3.gregale.dev/assets/key", nil)
	r.Header.Set("Origin", "https://console.example.test")
	r.Header.Set("Access-Control-Request-Method", "PUT")
	r.Header.Set("Access-Control-Request-Headers", "authorization,x-amz-meta-owner,x-amz-checksum-crc32,if-none-match")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "https://console.example.test" || len(store.admitted) != 0 {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	r.Header.Set("Origin", "https://evil.example.test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	h.enabled = func() bool { return false }
	r = signedGatewayRequest(t, http.MethodDelete, "https://s3.gregale.dev/assets/key", nil, "UNSIGNED-PAYLOAD")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || len(p.deleted) != 1 || len(store.admitted) != 0 {
		t.Fatalf("cleanup=%d admissions=%v", w.Code, store.admitted)
	}
	r = signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key", []byte("data"), "UNSIGNED-PAYLOAD")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest(http.MethodDelete, "https://s3.gregale.dev/assets/key", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || len(p.deleted) != 1 {
		t.Fatal("unauthenticated cleanup allowed")
	}
}

func TestGatewayMultipartMarkersAndTotalLimit(t *testing.T) {
	h, store, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected data request"); return nil, nil })
	sessions := newGatewayMultipartStore()
	h.multipartStore = sessions
	alpha, beta := uuid.NewString(), uuid.NewString()
	for id, key := range map[string]string{alpha: "alpha file", beta: "beta file"} {
		sessions.uploads[id] = state.ObjectMultipartUpload{ID: id, AccountID: store.bucket.AccountID, AppID: store.bucket.AppID, BucketID: store.bucket.ID, Key: key, ProviderUploadID: "provider", State: state.ObjectMultipartActive, ExpiresAt: h.now().Add(time.Hour)}
	}
	r := signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets?uploads=&max-uploads=1&encoding-type=url", nil, "UNSIGNED-PAYLOAD")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result listMultipartUploadsResult
	if e := xml.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if w.Code != 200 || len(result.Uploads) != 1 || !result.IsTruncated || result.NextKeyMarker != "alpha%20file" || result.NextUploadMarker != alpha {
		t.Fatalf("%d %+v", w.Code, result)
	}
	r = signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets?uploads=&max-uploads=1&key-marker=alpha%20file&upload-id-marker="+alpha, nil, "UNSIGNED-PAYLOAD")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if e := xml.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), "beta file") || strings.Contains(w.Body.String(), "<IsTruncated>true") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	h.registry.MaxUploadBytes = 100 << 20
	h.registry.MaxPartBytes = 64 << 20
	p.multipart = map[string]map[int32]objectstorage.MultipartPart{"provider": {1: {PartNumber: 1, ETag: "one", SizeBytes: 64 << 20}, 2: {PartNumber: 2, ETag: "two", SizeBytes: 1 << 20}}}
	body := []byte(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>one</ETag></Part><Part><PartNumber>2</PartNumber><ETag>two</ETag></Part></CompleteMultipartUpload>`)
	r = signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets/alpha%20file?uploadId="+alpha, body, "UNSIGNED-PAYLOAD")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || len(sessions.completionGrants) != 1 || sessions.completionGrants[0] != 65<<20 {
		t.Fatalf("multipart completion=%d %s reservations=%v", w.Code, w.Body.String(), sessions.completionGrants)
	}
	h.enabled = func() bool { return false }
	r = signedGatewayRequest(t, http.MethodDelete, "https://s3.gregale.dev/assets/beta%20file?uploadId="+beta, nil, "UNSIGNED-PAYLOAD")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || len(p.abortedUploads) != 1 {
		t.Fatalf("disabled abort=%d %s", w.Code, w.Body.String())
	}
}
func TestGatewayMultipartCapacityFailureStopsProvider(t *testing.T) {
	h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("quota denied part reached provider")
		return nil, nil
	})
	sessions := newGatewayMultipartStore()
	h.multipartStore = sessions
	sessions.capacityError = state.ErrObjectCapacity
	id := uuid.NewString()
	sessions.uploads[id] = state.ObjectMultipartUpload{ID: id, AccountID: store.bucket.AccountID, AppID: store.bucket.AppID, BucketID: store.bucket.ID, Key: "key", ProviderUploadID: "provider", State: state.ObjectMultipartActive, ExpiresAt: h.now().Add(time.Hour)}
	r := signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key?uploadId="+id+"&partNumber=1", []byte("data"), "UNSIGNED-PAYLOAD")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 409 || len(sessions.partGrants) != 1 || sessions.partGrants[0] != 4 {
		t.Fatalf("%d %s grants=%v", w.Code, w.Body.String(), sessions.partGrants)
	}
}
