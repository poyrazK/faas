package s3gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type multipartCopyIntegrationStore interface {
	Store
	state.ObjectBucketStore
	state.ObjectStorageAccountingStore
	state.ObjectStorageProviderUsageStore
	state.ObjectMultipartUploadStore
	state.ObjectMultipartTransferStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
}

// This HTTP fixture stores virtual source sizes so a >5 GiB source can exercise
// range admission without allocating its body. Every customer/provider call
// still crosses the real SDK, SigV4, gateway, adapter and persisted state.
type multipartCopyHTTPProvider struct {
	mu               sync.Mutex
	uploads          map[string]map[string]int
	sequence, copies int
	failure          string
	completed        bool
	sourceSize       int64
	sourceVersion    string
	sourceModified   time.Time
	latestChanged    bool
}

func (p *multipartCopyHTTPProvider) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/xml")
	switch {
	case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/source"):
		size := p.sourceSize
		if size == 0 {
			size = api.MaxObjectSinglePutBytes + 1
		}
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("ETag", `"source"`)
		w.Header().Set("X-Amz-Version-Id", p.sourceVersion)
		if !p.sourceModified.IsZero() {
			w.Header().Set("Last-Modified", p.sourceModified.Format(http.TimeFormat))
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("X-Amz-Meta-Owner", "original")
	case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "" && !q.Has("uploadId"):
		p.copies++
		if !p.checkSourceCopy(t, w, r) {
			return
		}
		if r.Header.Get("Content-Type") != "image/png" || r.Header.Get("X-Amz-Meta-Owner") != "original" {
			t.Error("version copy lost measured metadata")
		}
		if p.failure == "lost_ack" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("X-Amz-Copy-Source-Version-Id", p.sourceVersion)
		_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;copied&quot;</ETag><LastModified>2026-10-02T10:00:00Z</LastModified></CopyObjectResult>`)
	case r.Method == http.MethodGet && q.Has("uploads"):
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	case r.Method == http.MethodPost && q.Has("uploads"):
		p.sequence++
		id := fmt.Sprintf("private-upload-%d", p.sequence)
		p.uploads[id] = map[string]int{}
		_, _ = fmt.Fprintf(w, `<InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, id)
	case q.Get("uploadId") != "":
		p.serveUpload(t, w, r)
	case r.Method == http.MethodGet && p.completed:
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("ETag", `"object"`)
		_, _ = io.WriteString(w, "abcdefghij")
	default:
		t.Errorf("unexpected local provider request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	}
}

func (p *multipartCopyHTTPProvider) serveUpload(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	q := r.URL.Query()
	id := q.Get("uploadId")
	parts, exists := p.uploads[id]
	if !exists {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
		return
	}
	switch r.Method {
	case http.MethodPut:
		p.copies++
		if !p.checkSourceCopy(t, w, r) {
			return
		}
		if r.Header.Get("X-Amz-Copy-Source-Range") != "bytes=0-9" {
			t.Error("part copy not bound to measured private source/range")
		}
		if p.failure == "source_changed" {
			w.WriteHeader(412)
			_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code></Error>`)
			return
		}
		parts[q.Get("partNumber")] = 10
		if p.failure == "lost_ack" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		_, _ = io.WriteString(w, `<CopyPartResult><ETag>&quot;part&quot;</ETag><LastModified>2026-10-02T10:00:00Z</LastModified></CopyPartResult>`)
	case http.MethodGet:
		_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated>`)
		for part, size := range parts {
			_, _ = fmt.Fprintf(w, `<Part><PartNumber>%s</PartNumber><ETag>&quot;part&quot;</ETag><Size>%d</Size></Part>`, part, size)
		}
		_, _ = io.WriteString(w, `</ListPartsResult>`)
	case http.MethodPost:
		delete(p.uploads, id)
		p.completed = true
		_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>&quot;object&quot;</ETag></CompleteMultipartUploadResult>`)
	case http.MethodDelete:
		delete(p.uploads, id)
		w.WriteHeader(204)
	default:
		t.Error("unexpected multipart provider method", r.Method)
		w.WriteHeader(500)
	}
}

// The fixture evaluates provider-side predicates on the requested immutable
// source, independent of gateway admission. Changing the latest source leaves
// the retained version usable and detects accidentally copying the latest.
func (p *multipartCopyHTTPProvider) checkSourceCopy(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	path, rawQuery, _ := strings.Cut(r.Header.Get("X-Amz-Copy-Source"), "?")
	source, err := url.PathUnescape(path)
	query, qe := url.ParseQuery(rawQuery)
	version := p.sourceVersion
	if err != nil || qe != nil || source != "physical/source" || query.Get("versionId") != version || len(query) > 1 {
		t.Error("copy did not select the inspected version", source, query)
	}
	if p.latestChanged && query.Get("versionId") == "" {
		t.Error("copy selected changed latest source")
	}
	match := r.Header.Get("X-Amz-Copy-Source-If-Match")
	none := r.Header.Get("X-Amz-Copy-Source-If-None-Match")
	if p.sourceVersion == "" && match != `"source"` {
		t.Error("mutable source lost measured ETag fence")
	}
	if p.failure == "source_deleted" {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchVersion</Code><Message>private-version</Message></Error>`)
		return false
	}
	if match != "" && match != `"source"` || none == `"source"` || none == "*" {
		w.WriteHeader(412)
		_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code></Error>`)
		return false
	}
	if value := r.Header.Get("X-Amz-Copy-Source-If-Unmodified-Since"); value != "" && match == "" {
		date, err := http.ParseTime(value)
		if err != nil {
			t.Error(err)
		}
		if p.sourceModified.After(date) {
			w.WriteHeader(412)
			_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code></Error>`)
			return false
		}
	}
	if value := r.Header.Get("X-Amz-Copy-Source-If-Modified-Since"); value != "" {
		date, err := http.ParseTime(value)
		if err != nil {
			t.Error(err)
		}
		if !p.sourceModified.After(date) {
			w.WriteHeader(412)
			_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code></Error>`)
			return false
		}
	}
	return true
}

type multipartCopyIntegration struct {
	nativeEndpoint string
	handler        *Handler
	client         *awss3.Client
	store          multipartCopyIntegrationStore
	bucket         state.ObjectBucket
	provider       *multipartCopyHTTPProvider
}

func newMultipartCopyIntegration(t *testing.T, st multipartCopyIntegrationStore, permissions ...string) *multipartCopyIntegration {
	return newMultipartCopyIntegrationWithProvider(t, st, nil, permissions...)
}

func newMultipartCopyIntegrationWithProvider(t *testing.T, st multipartCopyIntegrationStore, handler http.Handler, permissions ...string) *multipartCopyIntegration {
	return newMultipartCopyIntegrationWithTransfer(t, st, handler, objectstorage.Config{}, api.MaxObjectSinglePutBytes+1, permissions...)
}

func newMultipartCopyIntegrationWithTransfer(t *testing.T, st multipartCopyIntegrationStore, handler http.Handler, config objectstorage.Config, initialBytes int64, permissions ...string) *multipartCopyIntegration {
	return newMultipartCopyIntegrationConfigured(t, st, handler, config, initialBytes, nil, permissions...)
}

func newMultipartCopyIntegrationConfigured(t *testing.T, st multipartCopyIntegrationStore, handler http.Handler, config objectstorage.Config, initialBytes int64, encryption func(string, string) objectstorage.EncryptionConfig, permissions ...string) *multipartCopyIntegration {
	t.Helper()
	p := &multipartCopyHTTPProvider{uploads: map[string]map[string]int{}}
	if handler == nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) })
	}
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	policy := api.ObjectStoragePolicy{MaxAccountBytes: api.MaxObjectSinglePutBytes + 21, MaxBucketBytes: api.MaxObjectSinglePutBytes + 21, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	if config.Accounting == nil {
		config.Accounting = &policy
	}
	acct, err := st.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "copy-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	config.DefaultRegion, config.Defaults = "us-east-1", map[string]string{"us-east-1": "local"}
	config.Backends = []objectstorage.BackendConfig{{ID: "local", Driver: "s3", Region: "us-east-1", Namespace: "integration", Endpoint: upstream.URL, AllowHTTP: true, PathStyle: true, S3Region: "us-east-1", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}}
	if encryption != nil {
		config.Backends[0].Encryption = encryption(acct.ID, upstream.URL)
	}
	registry, err := objectstorage.NewRegistry(config, func(string) string { return "local-provider-test-credential" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Name: "assets", Scope: "default", PhysicalName: "physical", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(t.Context(), acct.ID, app.ID, b.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(t.Context(), b.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}
	b.State = "ready"
	permission := state.ObjectBucketPermissionReadWrite
	if len(permissions) > 0 {
		permission = permissions[0]
	}
	if _, err = st.CreateObjectS3Credential(t.Context(), state.ObjectS3Credential{ID: uuid.NewString(), AccountID: acct.ID, BucketID: b.ID, AccessKeyID: testAccess, SecretSealed: []byte("sealed"), KID: "test", Label: "integration", Permission: permission, Status: state.ObjectS3CredentialStatusActive}, 10); err != nil {
		t.Fatal(err)
	}
	if err = st.ClaimObjectInventory(t.Context(), b.ID, "initial"); err != nil {
		t.Fatal(err)
	}
	keys := int64(1)
	if initialBytes == 0 {
		keys = 0
	}
	if err = st.FinishObjectInventory(t.Context(), b.ID, "initial", initialBytes, keys); err != nil {
		t.Fatal(err)
	}
	if err = st.RecordObjectUsageReport(t.Context(), api.ObjectStorageUsageReport{AccountID: acct.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Registry: registry, Store: st, RequestMetrics: st, Host: "s3.gregale.dev", Region: "us-east-1", SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1, HTTPClient: upstream.Client(), OpenSecret: func([]byte) (string, error) { return testSecret, nil }})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	endpoint, _ := url.Parse(server.URL)
	h.host = endpoint.Host
	client := awss3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(testAccess, testSecret, ""), HTTPClient: server.Client()}, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
		o.RetryMaxAttempts = 1
	})
	return &multipartCopyIntegration{nativeEndpoint: upstream.URL, handler: h, client: client, store: st, bucket: b, provider: p}
}

func (f *multipartCopyIntegration) initiate(t *testing.T, key string) string {
	t.Helper()
	out, err := f.client.CreateMultipartUpload(t.Context(), &awss3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String(key), ContentType: aws.String("text/plain")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = uuid.Parse(aws.ToString(out.UploadId)); err != nil {
		t.Fatal("private provider upload ID exposed")
	}
	return aws.ToString(out.UploadId)
}

func (f *multipartCopyIntegration) copyPart(t *testing.T, key, id string, part int32, match string) (*awss3.UploadPartCopyOutput, error) {
	t.Helper()
	return f.client.UploadPartCopy(t.Context(), &awss3.UploadPartCopyInput{Bucket: aws.String("assets"), Key: aws.String(key), UploadId: aws.String(id), PartNumber: aws.Int32(part), CopySource: aws.String("assets/source"), CopySourceRange: aws.String("bytes=0-9"), CopySourceIfMatch: stringPtr(match)})
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func assertSDKErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	if strings.Contains(err.Error(), "private-upload") || strings.Contains(err.Error(), "physical") {
		t.Fatal("provider identity leaked")
	}
}

// adr: 538
func TestMultipartCopyEndToEndMem(t *testing.T) { multipartCopyEndToEnd(t, state.NewMemStore()) }
func TestMultipartCopyEndToEndPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	multipartCopyEndToEnd(t, st)
}

func multipartCopyPGStore(t *testing.T) (*state.PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool), pool
}

func multipartCopyEndToEnd(t *testing.T, st multipartCopyIntegrationStore) {
	f := newMultipartCopyIntegration(t, st)
	id := f.initiate(t, "destination")
	_, err := f.copyPart(t, "destination", id, 1, `"other"`)
	assertSDKErrorCode(t, err, "PreconditionFailed")
	out, err := f.copyPart(t, "destination", id, 1, `"source"`)
	if err != nil || out.CopyPartResult == nil || aws.ToString(out.CopyPartResult.ETag) != `"part"` {
		t.Fatal(out, err)
	}
	listed, err := f.client.ListParts(t.Context(), &awss3.ListPartsInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id)})
	if err != nil || len(listed.Parts) != 1 || aws.ToInt64(listed.Parts[0].Size) != 10 {
		t.Fatal(listed, err)
	}
	_, err = f.client.CompleteMultipartUpload(t.Context(), &awss3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: out.CopyPartResult.ETag}}}})
	if err != nil {
		t.Fatal(err)
	}
	read, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination")})
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(read.Body)
	closeErr := read.Body.Close()
	if readErr != nil || closeErr != nil || string(data) != "abcdefghij" {
		t.Fatal(string(data), readErr, closeErr)
	}
	usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].GrantedBytes != 10 || usage.Buckets[0].MultipartBytes != 0 {
		t.Fatal(usage, err)
	}
	abortID := f.initiate(t, "aborted")
	if _, err = f.copyPart(t, "aborted", abortID, 1, ""); err != nil {
		t.Fatal(err)
	}
	_, err = f.copyPart(t, "aborted", abortID, 2, "")
	assertSDKErrorCode(t, err, "OperationAborted")
	_, err = f.client.AbortMultipartUpload(t.Context(), &awss3.AbortMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("aborted"), UploadId: aws.String(abortID)})
	if err != nil {
		t.Fatal(err)
	}
	usage, err = st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].GrantedBytes != 10 || usage.Buckets[0].MultipartBytes != 0 {
		t.Fatal("abort did not release only copied-part capacity", usage, err)
	}
	f.provider.mu.Lock()
	copies := f.provider.copies
	f.provider.mu.Unlock()
	if copies != 2 {
		t.Fatal("quota or predicate failure dispatched a copy", copies)
	}
}

func TestMultipartCopyUncertainAcknowledgmentMem(t *testing.T) {
	multipartCopyUncertain(t, state.NewMemStore())
}
func TestMultipartCopyUncertainAcknowledgmentPG(t *testing.T) {
	st, pool := multipartCopyPGStore(t)
	f := multipartCopyUncertain(t, st)
	restarted := state.NewPgStore(pool)
	u, err := restarted.GetObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, f.uploadID)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.BeginObjectMultipartPart(t.Context(), f.bucket.AccountID, f.bucket.ID, u.ID, "restart", 1, 10, f.handler.registry.MaxUploadBytes, f.handler.registry.Accounting); !errors.Is(err, state.ErrConflict) {
		t.Fatal("restart lost transfer fence", err)
	}
	if ready, err := restarted.ObjectMultipartAbortReady(t.Context(), u.ID, u.LeaseToken); err != nil || ready {
		t.Fatal("restart allowed abort through uncertain copy", ready, err)
	}
}

type uncertainMultipartCopy struct {
	*multipartCopyIntegration
	uploadID string
}

func multipartCopyUncertain(t *testing.T, st multipartCopyIntegrationStore) uncertainMultipartCopy {
	f := newMultipartCopyIntegration(t, st)
	id := f.initiate(t, "uncertain")
	f.provider.mu.Lock()
	f.provider.failure = "lost_ack"
	f.provider.mu.Unlock()
	_, err := f.copyPart(t, "uncertain", id, 1, "")
	assertSDKErrorCode(t, err, "ServiceUnavailable")
	_, err = f.copyPart(t, "uncertain", id, 1, "")
	assertSDKErrorCode(t, err, "OperationAborted")
	if err = st.BeginObjectMultipartPart(t.Context(), f.bucket.AccountID, f.bucket.ID, id, "another-token", 1, 10, f.handler.registry.MaxUploadBytes, f.handler.registry.Accounting); !errors.Is(err, state.ErrConflict) {
		t.Fatal("uncertain write fence lost", err)
	}
	_, err = f.client.CompleteMultipartUpload(t.Context(), &awss3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("uncertain"), UploadId: aws.String(id), MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: aws.String(`"part"`)}}}})
	assertSDKErrorCode(t, err, "OperationAborted")
	claimed, err := st.ClaimObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, id, "blocked-abort", state.ObjectMultipartAborting, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := st.ObjectMultipartAbortReady(t.Context(), id, claimed.LeaseToken); err != nil || ready {
		t.Fatal("uncertain copy allowed verified abort", ready, err)
	}
	usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].MultipartBytes != 10 {
		t.Fatal("uncertain copy capacity refunded", usage, err)
	}
	f.provider.mu.Lock()
	copies := f.provider.copies
	f.provider.mu.Unlock()
	if copies != 1 {
		t.Fatal("uncertain copy replayed", copies)
	}
	return uncertainMultipartCopy{f, id}
}
