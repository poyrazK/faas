package s3gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	testAccess = "GRGAAAAAAAAAAAAAAAAA"
	testSecret = "0123456789012345678901234567890123456789"
)

type gatewayTestStore struct {
	credential state.ObjectS3Credential
	bucket     state.ObjectBucket
	touched    int
	admitted   []string
}

func (s *gatewayTestStore) CreateObjectS3Credential(context.Context, state.ObjectS3Credential, int) (state.ObjectS3Credential, error) {
	panic("not used")
}
func (s *gatewayTestStore) ListObjectS3Credentials(context.Context, string, string) ([]state.ObjectS3Credential, error) {
	panic("not used")
}
func (s *gatewayTestStore) RevokeObjectS3Credential(context.Context, string, string, string) error {
	panic("not used")
}
func (s *gatewayTestStore) ResolveObjectS3Credential(_ context.Context, access string) (state.ObjectS3Credential, state.ObjectBucket, error) {
	if access != s.credential.AccessKeyID {
		return state.ObjectS3Credential{}, state.ObjectBucket{}, state.ErrNotFound
	}
	return s.credential, s.bucket, nil
}
func (s *gatewayTestStore) TouchObjectS3Credential(context.Context, string, time.Time) error {
	s.touched++
	return nil
}
func (s *gatewayTestStore) AdmitObjectURL(_ context.Context, _, _, key string, _ int64, _ bool, _ api.ObjectStoragePolicy) error {
	s.admitted = append(s.admitted, key)
	return nil
}

type gatewayTestProvider struct {
	objects          objectstorage.ObjectPage
	deleted          string
	multipart        map[string]map[int32]objectstorage.MultipartPart
	completedUploads []string
	abortedUploads   []string
}

type gatewayRequestMetrics struct {
	calls int
	err   error
}

func (m *gatewayRequestMetrics) RecordObjectStorageProviderRequest(context.Context, string, time.Time) error {
	m.calls++
	return m.err
}
func (*gatewayRequestMetrics) ListObjectStorageProviderRequestMetrics(context.Context, string, string, time.Time) ([]state.ObjectStorageProviderRequestMetric, error) {
	return nil, nil
}
func (*gatewayRequestMetrics) ListObjectStorageProviderBuckets(context.Context, string, string) ([]state.ObjectBucket, error) {
	return nil, nil
}

func (*gatewayTestProvider) CreateBucket(context.Context, string) error { return nil }
func (*gatewayTestProvider) DeleteBucket(context.Context, string) error { return nil }
func (p *gatewayTestProvider) ListObjects(context.Context, string, string, string, int32) (objectstorage.ObjectPage, error) {
	return p.objects, nil
}
func (p *gatewayTestProvider) DeleteObject(_ context.Context, _ string, key string) error {
	p.deleted = key
	return nil
}
func (*gatewayTestProvider) Presign(_ context.Context, bucket string, request objectstorage.SignRequest) (objectstorage.SignedRequest, error) {
	return objectstorage.SignedRequest{URL: "https://provider.invalid/" + bucket + "/" + request.Key, Method: request.Method, Headers: map[string]string{"Content-Type": request.ContentType}}, nil
}
func (p *gatewayTestProvider) EnsureMultipartUpload(_ context.Context, _ string, r objectstorage.MultipartCreateRequest) (string, error) {
	if p.multipart == nil {
		p.multipart = map[string]map[int32]objectstorage.MultipartPart{}
	}
	const id = "provider-upload-id"
	if p.multipart[id] == nil {
		p.multipart[id] = map[int32]objectstorage.MultipartPart{}
	}
	return id, nil
}
func (p *gatewayTestProvider) PresignMultipartPart(_ context.Context, _ string, r objectstorage.MultipartPartRequest) (objectstorage.SignedRequest, error) {
	if p.multipart == nil {
		p.multipart = map[string]map[int32]objectstorage.MultipartPart{}
	}
	if p.multipart[r.ProviderUploadID] == nil {
		p.multipart[r.ProviderUploadID] = map[int32]objectstorage.MultipartPart{}
	}
	p.multipart[r.ProviderUploadID][r.PartNumber] = objectstorage.MultipartPart{
		PartNumber: r.PartNumber, ETag: `"etag-` + strconv.FormatInt(int64(r.PartNumber), 10) + `"`, SizeBytes: r.SizeBytes,
		LastModified: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
	}
	return objectstorage.SignedRequest{URL: "https://provider.invalid/upload/" + r.ProviderUploadID + "/" + strconv.FormatInt(int64(r.PartNumber), 10), Method: http.MethodPut, Headers: map[string]string{"Content-Length": strconv.FormatInt(r.SizeBytes, 10)}}, nil
}
func (p *gatewayTestProvider) ListMultipartParts(_ context.Context, _ string, r objectstorage.MultipartListPartsRequest) (objectstorage.MultipartPartsPage, error) {
	parts := make([]objectstorage.MultipartPart, 0, len(p.multipart[r.ProviderUploadID]))
	for _, part := range p.multipart[r.ProviderUploadID] {
		if part.PartNumber > r.PartNumberMarker {
			parts = append(parts, part)
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })
	if len(parts) > int(r.Limit) {
		next := parts[r.Limit-1].PartNumber
		return objectstorage.MultipartPartsPage{Items: parts[:r.Limit], NextPartNumberMarker: next}, nil
	}
	return objectstorage.MultipartPartsPage{Items: parts}, nil
}
func (p *gatewayTestProvider) CompleteMultipartUpload(_ context.Context, _ string, r objectstorage.MultipartCompleteRequest) error {
	p.completedUploads = append(p.completedUploads, r.ProviderUploadID)
	return nil
}
func (p *gatewayTestProvider) AbortMultipartUpload(_ context.Context, _ string, r objectstorage.MultipartAbortRequest) error {
	p.abortedUploads = append(p.abortedUploads, r.ProviderUploadID)
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type gatewayMultipartStore struct {
	mu      sync.Mutex
	uploads map[string]state.ObjectMultipartUpload
}

func newGatewayMultipartStore() *gatewayMultipartStore {
	return &gatewayMultipartStore{uploads: map[string]state.ObjectMultipartUpload{}}
}

func (s *gatewayMultipartStore) ReserveObjectMultipartUpload(_ context.Context, upload state.ObjectMultipartUpload, _ int) (state.ObjectMultipartUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.uploads {
		if existing.Key == upload.Key && existing.BucketID == upload.BucketID && existing.State != state.ObjectMultipartCompleted && existing.State != state.ObjectMultipartAborted {
			return existing, nil
		}
	}
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	upload.State, upload.CreatedAt, upload.UpdatedAt = state.ObjectMultipartInitiating, now, now
	upload.Parts = []api.ObjectMultipartCompletedPart{}
	s.uploads[upload.ID] = upload
	return upload, nil
}

func (s *gatewayMultipartStore) ListObjectMultipartUploads(_ context.Context, account, app, bucket string, limit int32, _ string) ([]state.ObjectMultipartUpload, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]state.ObjectMultipartUpload, 0)
	for _, upload := range s.uploads {
		if upload.AccountID == account && upload.AppID == app && upload.BucketID == bucket {
			rows = append(rows, upload)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) > int(limit) {
		return rows[:limit], rows[limit-1].ID, nil
	}
	return rows, "", nil
}

func (s *gatewayMultipartStore) GetObjectMultipartUpload(_ context.Context, account, app, bucket, id string) (state.ObjectMultipartUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	upload, ok := s.uploads[id]
	if !ok || upload.AccountID != account || upload.AppID != app || upload.BucketID != bucket {
		return state.ObjectMultipartUpload{}, state.ErrNotFound
	}
	return upload, nil
}

func (s *gatewayMultipartStore) ClaimObjectMultipartUpload(_ context.Context, account, app, bucket, id, token, operation string, parts []api.ObjectMultipartCompletedPart, _ bool) (state.ObjectMultipartUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	upload, ok := s.uploads[id]
	if !ok || upload.AccountID != account || upload.AppID != app || upload.BucketID != bucket || token == "" {
		return state.ObjectMultipartUpload{}, state.ErrConflict
	}
	if operation == state.ObjectMultipartInitiating && upload.State != state.ObjectMultipartInitiating || operation == state.ObjectMultipartCompleting && upload.State != state.ObjectMultipartActive || operation == state.ObjectMultipartAborting && upload.State != state.ObjectMultipartActive {
		return state.ObjectMultipartUpload{}, state.ErrConflict
	}
	upload.State, upload.LeaseToken = operation, token
	if operation == state.ObjectMultipartCompleting {
		upload.Parts = append([]api.ObjectMultipartCompletedPart(nil), parts...)
	}
	s.uploads[id] = upload
	return upload, nil
}

func (s *gatewayMultipartStore) ActivateObjectMultipartUpload(_ context.Context, id, token, providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	upload, ok := s.uploads[id]
	if !ok || upload.State != state.ObjectMultipartInitiating || upload.LeaseToken != token || providerID == "" {
		return state.ErrConflict
	}
	upload.State, upload.ProviderUploadID, upload.LeaseToken = state.ObjectMultipartActive, providerID, ""
	s.uploads[id] = upload
	return nil
}

func (s *gatewayMultipartStore) SetObjectMultipartUploadSize(_ context.Context, id, token string, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	upload, ok := s.uploads[id]
	if !ok || upload.State != state.ObjectMultipartCompleting || upload.LeaseToken != token {
		return state.ErrConflict
	}
	upload.SizeBytes = size
	s.uploads[id] = upload
	return nil
}

func (s *gatewayMultipartStore) FinishObjectMultipartUpload(_ context.Context, id, token, next string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	upload, ok := s.uploads[id]
	if !ok || upload.LeaseToken != token || next != state.ObjectMultipartCompleted && next != state.ObjectMultipartAborted {
		return state.ErrConflict
	}
	upload.State, upload.LeaseToken = next, ""
	s.uploads[id] = upload
	return nil
}

func (*gatewayMultipartStore) RetryObjectMultipartUpload(context.Context, string, string, string, time.Duration) error {
	return state.ErrConflict
}

func (*gatewayMultipartStore) DueObjectMultipartUploads(context.Context, int32) ([]state.ObjectMultipartUpload, error) {
	return nil, nil
}

func newGatewayTestHandler(t *testing.T, permission string, roundTrip roundTripFunc) (*Handler, *gatewayTestStore, *gatewayTestProvider) {
	t.Helper()
	provider := &gatewayTestProvider{objects: objectstorage.ObjectPage{Items: []objectstorage.Object{{Key: "folder/a.txt", Size: 3, LastModified: time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)}}}}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{
		DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, MaxUploadBytes: 16 << 20,
		Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "test", Region: "us-east-1", Namespace: "fixture"}},
	}, func(string) string { return "" }, map[string]objectstorage.Factory{"test": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	store := &gatewayTestStore{
		credential: state.ObjectS3Credential{ID: "credential", AccountID: "account", BucketID: "bucket-id", AccessKeyID: testAccess, SecretSealed: []byte("sealed"), Permission: permission, Status: state.ObjectS3CredentialStatusActive},
		bucket:     state.ObjectBucket{ID: "bucket-id", AccountID: "account", Name: "assets", PhysicalName: "gregale-physical", State: "ready", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	}
	handler, err := New(Config{
		Registry: registry, Store: store, Host: "s3.gregale.dev", Region: "us-east-1", SpoolDir: t.TempDir(),
		OpenSecret: func(sealed []byte) (string, error) {
			if string(sealed) != "sealed" {
				t.Fatal("unexpected sealed secret")
			}
			return testSecret, nil
		},
		HTTPClient: &http.Client{Transport: roundTrip, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		Now:        func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, provider
}

func signedGatewayRequest(t *testing.T, method, target string, body []byte, payloadHash string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Host = "s3.gregale.dev"
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	request.Header.Set("X-Amz-Date", "20260907T120000Z")
	if method == http.MethodPut {
		request.Header.Set("Content-Type", "application/octet-stream")
	}
	if err := awsv4.NewSigner().SignHTTP(context.Background(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, request, payloadHash, "s3", "us-east-1", time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return request
}

func presignedGatewayRequest(t *testing.T, method, target string, body []byte) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Host = "s3.gregale.dev"
	query := request.URL.Query()
	query.Set("X-Amz-Expires", "300")
	request.URL.RawQuery = query.Encode()
	signed, headers, err := awsv4.NewSigner().PresignHTTP(context.Background(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, request, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := http.NewRequest(method, signed, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = "s3.gregale.dev"
	for name, values := range headers {
		for _, value := range values {
			if !strings.EqualFold(name, "Host") {
				parsed.Header.Add(name, value)
			}
		}
	}
	return parsed
}

func TestGatewayPutAndGetHideProvider(t *testing.T) {
	var uploaded []byte
	handler, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			uploaded, _ = io.ReadAll(r.Body)
			header := make(http.Header)
			header.Set("ETag", `"etag"`)
			header.Set("x-goog-generation", "provider-leak")
			return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		header := make(http.Header)
		header.Set("Content-Type", "text/plain")
		header.Set("Content-Length", "5")
		header.Set("x-goog-generation", "provider-leak")
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader("hello")), Request: r}, nil
	})
	body := []byte("hello")
	sum := sha256.Sum256(body)
	put := signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/folder/hello.txt?x-id=PutObject", body, hex.EncodeToString(sum[:]))
	putRecorder := httptest.NewRecorder()
	handler.ServeHTTP(putRecorder, put)
	if putRecorder.Code != http.StatusOK || string(uploaded) != "hello" || putRecorder.Header().Get("ETag") != `"etag"` {
		t.Fatalf("PUT = %d headers=%v uploaded=%q body=%s", putRecorder.Code, putRecorder.Header(), uploaded, putRecorder.Body.String())
	}
	if putRecorder.Header().Get("x-goog-generation") != "" {
		t.Fatal("provider header escaped branded endpoint")
	}

	get := signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/folder/hello.txt?x-id=GetObject", nil, "UNSIGNED-PAYLOAD")
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, get)
	if getRecorder.Code != http.StatusOK || getRecorder.Body.String() != "hello" || getRecorder.Header().Get("x-goog-generation") != "" {
		t.Fatalf("GET = %d headers=%v body=%q", getRecorder.Code, getRecorder.Header(), getRecorder.Body.String())
	}
	if len(store.admitted) != 2 || store.admitted[0] != "folder/hello.txt" || store.touched != 1 {
		t.Fatalf("admission=%v touched=%d", store.admitted, store.touched)
	}
}

func TestGatewayBlocksProviderCallWhenRequestMetricCannotBeRecorded(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, func(*http.Request) (*http.Response, error) {
		t.Fatal("provider must not be called when request metrics fail")
		return nil, nil
	})
	metrics := &gatewayRequestMetrics{err: state.ErrConflict}
	handler.requestMetrics = metrics
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/key", nil, "UNSIGNED-PAYLOAD"))
	if recorder.Code != http.StatusServiceUnavailable || metrics.calls != 1 || !strings.Contains(recorder.Body.String(), "ServiceUnavailable") {
		t.Fatalf("response = %d %s calls=%d", recorder.Code, recorder.Body.String(), metrics.calls)
	}
}

func TestGatewayListsOnlyCredentialBucket(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, func(*http.Request) (*http.Response, error) {
		t.Fatal("list must use provider interface, not HTTP")
		return nil, nil
	})
	for _, target := range []string{"https://s3.gregale.dev/", "https://s3.gregale.dev/assets?list-type=2&max-keys=10&x-id=ListObjectsV2"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodGet, target, nil, "UNSIGNED-PAYLOAD"))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "assets") {
			t.Fatalf("GET %s = %d %s", target, recorder.Code, recorder.Body.String())
		}
		if err := xml.Unmarshal(recorder.Body.Bytes(), new(any)); err != nil {
			t.Fatalf("invalid XML: %v", err)
		}
	}
}

func TestGatewayRejectsBadBodyAndWrongBucket(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("rejected request reached provider")
		return nil, nil
	})
	other := sha256.Sum256([]byte("other"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/bad.txt", []byte("body"), hex.EncodeToString(other[:])))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "XAmzContentSHA256Mismatch") {
		t.Fatalf("bad digest = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/other/key", nil, "UNSIGNED-PAYLOAD"))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "NoSuchBucket") {
		t.Fatalf("wrong bucket = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayBoundsConcurrentPutStaging(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("throttled request reached provider")
		return nil, nil
	})
	for range cap(handler.putSlots) {
		handler.putSlots <- struct{}{}
	}
	body := []byte("body")
	sum := sha256.Sum256(body)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key", body, hex.EncodeToString(sum[:])))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "SlowDown") {
		t.Fatalf("concurrent PUT limit = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayPermissionAndUnsupportedMultipart(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, func(*http.Request) (*http.Response, error) {
		t.Fatal("rejected request reached provider")
		return nil, nil
	})
	body := []byte("body")
	sum := sha256.Sum256(body)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key", body, hex.EncodeToString(sum[:])))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "AccessDenied") {
		t.Fatalf("permission = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets/key?uploads=", nil, "UNSIGNED-PAYLOAD"))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "AccessDenied") {
		t.Fatalf("multipart permission = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayRejectsTamperedSignatureAndAcceptsPresignedRequests(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Length": {"5"}}, Body: io.NopCloser(strings.NewReader("hello")), Request: r}, nil
	})

	tampered := signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/file.txt", nil, "UNSIGNED-PAYLOAD")
	tampered.Header.Set("Authorization", strings.Replace(tampered.Header.Get("Authorization"), "Signature=", "Signature=00", 1))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, tampered)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "SignatureDoesNotMatch") {
		t.Fatalf("tampered signature = %d %s", recorder.Code, recorder.Body.String())
	}

	presigned := presignedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/file.txt", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, presigned)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "hello" {
		t.Fatalf("presigned query = %d %s", recorder.Code, recorder.Body.String())
	}

	presignedPut := presignedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/presigned.txt", []byte("body"))
	presignedPut.Header.Set("Content-Type", "application/octet-stream")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, presignedPut)
	if recorder.Code != http.StatusOK {
		t.Fatalf("presigned PUT = %d %s", recorder.Code, recorder.Body.String())
	}

	streaming := httptest.NewRequest(http.MethodPut, "https://s3.gregale.dev/assets/file.txt", strings.NewReader("chunked"))
	streaming.Host = "s3.gregale.dev"
	streaming.Header.Set("X-Amz-Content-Sha256", "STREAMING-AWS4-HMAC-SHA256-PAYLOAD")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, streaming)
	if recorder.Code != http.StatusNotImplemented || !strings.Contains(recorder.Body.String(), "NotImplemented") {
		t.Fatalf("streaming payload = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayPublicMultipartLifecycle(t *testing.T) {
	handler, _, provider := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/upload/") {
			part := strings.TrimPrefix(r.URL.Path, "/upload/provider-upload-id/")
			header := make(http.Header)
			header.Set("ETag", `"etag-`+part+`"`)
			return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		t.Fatalf("unexpected provider request: %s %s", r.Method, r.URL.String())
		return nil, nil
	})
	handler.multipartStore = newGatewayMultipartStore()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets/archive.bin?uploads=", nil, "UNSIGNED-PAYLOAD"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("initiate = %d %s", recorder.Code, recorder.Body.String())
	}
	var initiated initiateMultipartResult
	if err := xml.Unmarshal(recorder.Body.Bytes(), &initiated); err != nil || initiated.UploadID == "" {
		t.Fatalf("initiate response = %q %v", recorder.Body.String(), err)
	}

	for partNumber, body := range map[int]string{1: strings.Repeat("a", int(api.MinMultipartPartBytes)), 2: "world"} {
		target := "https://s3.gregale.dev/assets/archive.bin?partNumber=" + strconv.Itoa(partNumber) + "&uploadId=" + initiated.UploadID
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPut, target, []byte(body), "UNSIGNED-PAYLOAD"))
		if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"etag-`+strconv.Itoa(partNumber)+`"` {
			t.Fatalf("part %d = %d headers=%v body=%s", partNumber, recorder.Code, recorder.Header(), recorder.Body.String())
		}
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/archive.bin?uploadId="+initiated.UploadID, nil, "UNSIGNED-PAYLOAD"))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "<PartNumber>1</PartNumber>") || !strings.Contains(recorder.Body.String(), "<PartNumber>2</PartNumber>") {
		t.Fatalf("list parts = %d %s", recorder.Code, recorder.Body.String())
	}

	completeBody := []byte(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"etag-1"</ETag></Part><Part><PartNumber>2</PartNumber><ETag>"etag-2"</ETag></Part></CompleteMultipartUpload>`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets/archive.bin?uploadId="+initiated.UploadID, completeBody, "UNSIGNED-PAYLOAD"))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "CompleteMultipartUploadResult") || len(provider.completedUploads) != 1 {
		t.Fatalf("complete = %d %s completed=%v", recorder.Code, recorder.Body.String(), provider.completedUploads)
	}

	// A second upload can be initiated for a different key and aborted without
	// leaking the provider upload identifier through the public response.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets/discard.bin?uploads=", nil, "UNSIGNED-PAYLOAD"))
	var discarded initiateMultipartResult
	if recorder.Code != http.StatusOK || xml.Unmarshal(recorder.Body.Bytes(), &discarded) != nil {
		t.Fatalf("second initiate = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodDelete, "https://s3.gregale.dev/assets/discard.bin?uploadId="+discarded.UploadID, nil, "UNSIGNED-PAYLOAD"))
	if recorder.Code != http.StatusNoContent || len(provider.abortedUploads) != 1 {
		t.Fatalf("abort = %d %s aborted=%v", recorder.Code, recorder.Body.String(), provider.abortedUploads)
	}
}
