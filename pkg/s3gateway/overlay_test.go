package s3gateway

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// overlayTestProvider keeps objects per physical bucket and lists them the
// way S3 does, start-after and delimiter included.
type overlayTestProvider struct {
	*gatewayTestProvider
	mu      sync.Mutex
	buckets map[string]map[string]string
}

func (p *overlayTestProvider) ListObjectsV2(_ context.Context, bucket string, r objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var keys []string
	for key := range p.buckets[bucket] {
		if strings.HasPrefix(key, r.Prefix) && key > r.StartAfter {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	page := objectstorage.ObjectPage{}
	count := 0
	for i, key := range keys {
		prefix := ""
		if r.Delimiter != "" {
			if at := strings.Index(key[len(r.Prefix):], r.Delimiter); at >= 0 {
				prefix = key[:len(r.Prefix)+at+len(r.Delimiter)]
			}
		}
		if prefix != "" && len(page.CommonPrefixes) > 0 && page.CommonPrefixes[len(page.CommonPrefixes)-1] == prefix {
			continue
		}
		if count == int(r.Limit) {
			page.NextCursor = "provider-cursor-" + strconv.Itoa(i)
			break
		}
		count++
		if prefix != "" {
			page.CommonPrefixes = append(page.CommonPrefixes, prefix)
			continue
		}
		page.Items = append(page.Items, objectstorage.Object{Key: key, Size: int64(len(p.buckets[bucket][key])), ETag: `"` + key + `"`, LastModified: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)})
	}
	return page, nil
}

func (p *overlayTestProvider) DeleteMutableObject(_ context.Context, bucket, key, selector string) (objectstorage.MutableDeleteResult, error) {
	if selector != "" {
		return objectstorage.MutableDeleteResult{}, objectstorage.ErrUnsupported
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.buckets[bucket], key)
	p.deleted = append(p.deleted, bucket+"/"+key)
	return objectstorage.MutableDeleteResult{}, nil
}

func (p *overlayTestProvider) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := p.DeleteMutableObject(ctx, bucket, key, "")
	return err
}

// roundTrip serves presigned GETs and HEADs from the buckets.
func (p *overlayTestProvider) roundTrip(r *http.Request) (*http.Response, error) {
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	p.mu.Lock()
	body, ok := p.buckets[bucket][key]
	p.mu.Unlock()
	header := make(http.Header)
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	header.Set("ETag", `"`+key+`"`)
	header.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		body = ""
	}
	return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// overlayGatewayStore adds the MemStore's overlays to the gateway fixture.
type overlayGatewayStore struct {
	*gatewayTestStore
	mem *state.MemStore
}

func (s *overlayGatewayStore) PutObjectBucketOverlay(ctx context.Context, o state.ObjectBucketOverlay) (state.ObjectBucketOverlay, error) {
	return s.mem.PutObjectBucketOverlay(ctx, o)
}
func (s *overlayGatewayStore) ObjectBucketOverlay(ctx context.Context, bucketID string) (state.ObjectBucketOverlay, error) {
	return s.mem.ObjectBucketOverlay(ctx, bucketID)
}
func (s *overlayGatewayStore) AddObjectBucketOverlayTombstones(ctx context.Context, bucketID string, keys []string) error {
	return s.mem.AddObjectBucketOverlayTombstones(ctx, bucketID, keys)
}
func (s *overlayGatewayStore) ObjectBucketOverlayTombstones(ctx context.Context, bucketID string, keys []string) (map[string]bool, error) {
	return s.mem.ObjectBucketOverlayTombstones(ctx, bucketID, keys)
}
func (s *overlayGatewayStore) DeleteObjectBucketOverlay(ctx context.Context, bucketID string) error {
	return s.mem.DeleteObjectBucketOverlay(ctx, bucketID)
}

func readyGatewayBucket(t *testing.T, mem *state.MemStore, b state.ObjectBucket) state.ObjectBucket {
	t.Helper()
	b, err := mem.ReserveObjectBucket(t.Context(), b, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.ClaimObjectBucket(t.Context(), b.AccountID, b.AppID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := mem.FinishObjectBucket(t.Context(), b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	b.State = "ready"
	return b
}

// newOverlayGatewayHandler serves a branch bucket ("br-preview") whose
// overlay reads through to the parent's "assets".
func newOverlayGatewayHandler(t *testing.T, source, branch map[string]string) (*Handler, *overlayTestProvider) {
	t.Helper()
	provider := &overlayTestProvider{gatewayTestProvider: &gatewayTestProvider{}, buckets: map[string]map[string]string{
		"gregale-source": source, "gregale-branch": branch,
	}}
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
	mem := state.NewMemStore()
	account, err := mem.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "overlay-fixture", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	base := state.ObjectBucket{AccountID: account.ID, AppID: app.ID, Region: "us-east-1", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint}
	parent, child := base, base
	parent.ID, parent.Name, parent.Scope, parent.PhysicalName = uuid.NewString(), "assets", "production", "gregale-source"
	child.ID, child.Name, child.Scope, child.PhysicalName = uuid.NewString(), "br-preview", "preview", "gregale-branch"
	child.EnvironmentCloneSourceBucketID = parent.ID
	parent, child = readyGatewayBucket(t, mem, parent), readyGatewayBucket(t, mem, child)
	if _, err := mem.PutObjectBucketOverlay(t.Context(), state.ObjectBucketOverlay{BucketID: child.ID, AccountID: account.ID,
		SourceBucketID: parent.ID, SourceAppID: app.ID, Consistency: state.ObjectBucketOverlayLiveParent, AsOf: time.Now()}); err != nil {
		t.Fatal(err)
	}
	store := &overlayGatewayStore{mem: mem, gatewayTestStore: &gatewayTestStore{ObjectDeletionStore: mem, bucket: child,
		credential: state.ObjectS3Credential{ID: "credential", AccountID: account.ID, BucketID: child.ID, AccessKeyID: testAccess,
			SecretSealed: []byte("sealed"), Permission: state.ObjectBucketPermissionReadWrite, Status: state.ObjectS3CredentialStatusActive}}}
	handler, err := New(Config{
		Registry: registry, Store: store, Host: "s3.gregale.dev", Region: "us-east-1", SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1,
		OpenSecret: func([]byte) (string, error) { return testSecret, nil },
		HTTPClient: &http.Client{Transport: roundTripFunc(provider.roundTrip)},
		Now:        func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, provider
}

func serveOverlay(t *testing.T, handler *Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, method, target, nil, "UNSIGNED-PAYLOAD"))
	return recorder
}

func TestGatewayOverlayReadsThroughToTheParent(t *testing.T) {
	handler, provider := newOverlayGatewayHandler(t,
		map[string]string{"a.txt": "parent-a", "b.txt": "parent-b"},
		map[string]string{"b.txt": "branch-b"})
	for _, tc := range []struct {
		key, method string
		status      int
		body        string
	}{
		{"a.txt", http.MethodGet, http.StatusOK, "parent-a"},
		{"b.txt", http.MethodGet, http.StatusOK, "branch-b"},
		{"a.txt", http.MethodHead, http.StatusOK, ""},
		{"missing.txt", http.MethodGet, http.StatusNotFound, ""},
	} {
		got := serveOverlay(t, handler, tc.method, "https://s3.gregale.dev/br-preview/"+tc.key)
		if got.Code != tc.status || tc.status == http.StatusOK && got.Body.String() != tc.body {
			t.Fatalf("%s %s = %d %q, want %d %q", tc.method, tc.key, got.Code, got.Body.String(), tc.status, tc.body)
		}
		if got.Header().Get("X-Amz-Version-Id") != "" {
			t.Fatalf("%s %s leaked a version id", tc.method, tc.key)
		}
	}
	if got := serveOverlay(t, handler, http.MethodDelete, "https://s3.gregale.dev/br-preview/a.txt"); got.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", got.Code, got.Body.String())
	}
	if got := serveOverlay(t, handler, http.MethodGet, "https://s3.gregale.dev/br-preview/a.txt"); got.Code != http.StatusNotFound {
		t.Fatalf("a tombstoned key read through: %d %q", got.Code, got.Body.String())
	}
	if provider.buckets["gregale-source"]["a.txt"] != "parent-a" || !slices.Equal(provider.deleted, []string{"gregale-branch/a.txt"}) {
		t.Fatalf("the branch delete reached the parent: source=%v deleted=%v", provider.buckets["gregale-source"], provider.deleted)
	}
}

func listOverlayKeys(t *testing.T, handler *Handler, query url.Values) (keys []string, pages int) {
	t.Helper()
	token := ""
	for pages = 1; pages < 20; pages++ {
		q := url.Values{"list-type": {"2"}}
		for name, values := range query {
			q[name] = values
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		got := serveOverlay(t, handler, http.MethodGet, "https://s3.gregale.dev/br-preview?"+q.Encode())
		if got.Code != http.StatusOK {
			t.Fatalf("list = %d %s", got.Code, got.Body.String())
		}
		var result listBucketResult
		if err := xml.Unmarshal(got.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		for _, c := range result.Contents {
			keys = append(keys, c.Key)
		}
		for _, p := range result.CommonPrefixes {
			keys = append(keys, p.Prefix)
		}
		if !result.IsTruncated {
			return keys, pages
		}
		if !strings.HasPrefix(result.NextContinuationToken, overlayListCursorPrefix) {
			t.Fatalf("continuation token %q is the provider's", result.NextContinuationToken)
		}
		token = result.NextContinuationToken
	}
	t.Fatal("listing never finished")
	return nil, 0
}

func TestGatewayOverlayListsTheMergedBuckets(t *testing.T) {
	handler, _ := newOverlayGatewayHandler(t,
		map[string]string{"a.txt": "1", "b.txt": "2", "dir/c.txt": "3", "dir/d.txt": "4", "e.txt": "5", "gone.txt": "6"},
		map[string]string{"b.txt": "branch", "dir/x.txt": "7", "f.txt": "8"})
	if got := serveOverlay(t, handler, http.MethodDelete, "https://s3.gregale.dev/br-preview/gone.txt"); got.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", got.Code)
	}
	want := []string{"a.txt", "b.txt", "dir/c.txt", "dir/d.txt", "dir/x.txt", "e.txt", "f.txt"}
	if keys, _ := listOverlayKeys(t, handler, nil); !slices.Equal(keys, want) {
		t.Fatalf("listing = %v, want %v", keys, want)
	}
	for _, limit := range []string{"1", "2", "3"} {
		keys, pages := listOverlayKeys(t, handler, url.Values{"max-keys": {limit}})
		if !slices.Equal(keys, want) || pages < 3 {
			t.Fatalf("max-keys=%s listing = %v in %d page(s), want %v", limit, keys, pages, want)
		}
	}
	delimited := []string{"a.txt", "b.txt", "e.txt", "f.txt", "dir/"}
	for _, limit := range []string{"1", "1000"} {
		keys, _ := listOverlayKeys(t, handler, url.Values{"delimiter": {"/"}, "max-keys": {limit}})
		sort.Strings(keys)
		sort.Strings(delimited)
		if !slices.Equal(keys, delimited) {
			t.Fatalf("delimited max-keys=%s listing = %v, want %v", limit, keys, delimited)
		}
	}
	if keys, _ := listOverlayKeys(t, handler, url.Values{"prefix": {"dir/"}, "start-after": {"dir/c.txt"}}); !slices.Equal(keys, []string{"dir/d.txt", "dir/x.txt"}) {
		t.Fatalf("prefix listing = %v", keys)
	}
	q := url.Values{"list-type": {"2"}, "continuation-token": {overlayListCursorPrefix + "!!"}}
	if got := serveOverlay(t, handler, http.MethodGet, "https://s3.gregale.dev/br-preview?"+q.Encode()); got.Code != http.StatusBadRequest {
		t.Fatalf("a forged token = %d", got.Code)
	}
}
