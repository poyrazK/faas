package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type snapshotNativeCopyFixture struct {
	mu                           sync.Mutex
	copies, attempts             int
	receipt                      string
	failure                      string
	failed                       bool
	failedReads                  int
	missing                      atomic.Int32
	concurrentMissingReadBarrier chan struct{}
}

func (f *snapshotNativeCopyFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Method == http.MethodHead && r.URL.Path == "/destination/key" && f.concurrentMissingReadBarrier != nil && f.missing.Load() < 2 {
		if f.missing.Add(1) == 2 {
			close(f.concurrentMissingReadBarrier)
		}
		select {
		case <-f.concurrentMissingReadBarrier:
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(404)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodHead && r.URL.Path == "/source/key":
		if r.URL.Query().Get("versionId") != "v1" {
			t.Error("source HEAD was not version-pinned", r.URL)
		}
		w.Header().Set("Content-Length", "4")
		w.Header().Set("ETag", `"source"`)
		w.Header().Set("X-Amz-Version-Id", "v1")
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("X-Amz-Meta-Owner", "customer")
	case r.Method == http.MethodHead && r.URL.Path == "/destination/key":
		if f.copies == 0 {
			w.WriteHeader(404)
			return
		}
		if f.failure == "readback" && !f.failed {
			f.failed = true
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Length", "4")
		w.Header().Set("ETag", `"target"`)
		w.Header().Set("X-Amz-Version-Id", "target-v1")
		w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, f.receipt)
	case r.Method == http.MethodPut && r.URL.Path == "/destination/key":
		f.attempts++
		if r.Header.Get("If-None-Match") != "*" || !strings.Contains(r.Header.Get("X-Amz-Copy-Source"), "versionId=v1") || r.Header.Get("X-Amz-Copy-Source-If-Match") != `"source"` {
			t.Error("copy lost its atomic destination or immutable source condition", r.Header)
		}
		if f.copies != 0 {
			w.WriteHeader(412)
			_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code></Error>`)
			return
		}
		if r.Header.Get("X-Amz-Metadata-Directive") != "REPLACE" || r.Header.Get("X-Amz-Meta-Owner") != "customer" || r.Header.Get("Content-Type") != "text/plain" || r.Header.Get("Cache-Control") != "max-age=60" || r.Header.Get("X-Amz-Tagging-Directive") != "COPY" {
			t.Error("snapshot copy lost source metadata or tags", r.Header)
		}
		f.receipt = r.Header.Get("X-Amz-Meta-" + ReservedUploadReceiptMetadataKey)
		if f.receipt == "" {
			t.Error("copy has no recovery proof")
		}
		f.copies++
		if f.failure == "acknowledgment" {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
			return
		}
		w.Header().Set("X-Amz-Version-Id", "target-v1")
		_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;target&quot;</ETag></CopyObjectResult>`)
	case r.Method == http.MethodGet:
		version := "v1"
		if r.URL.Path == "/destination/key" {
			version = "target-v1"
		}
		if r.URL.Query().Get("versionId") != version {
			t.Error("verification read was not version-pinned", r.URL)
		}
		if f.failure == "verification" && f.failedReads < 2 {
			f.failedReads++
			w.WriteHeader(503)
			return
		}
		_, _ = io.WriteString(w, "data")
	default:
		t.Error("unexpected snapshot request", r.Method, r.URL)
		w.WriteHeader(500)
	}
}

func snapshotCopyTestProvider(t *testing.T, endpoint string) ObjectSnapshotCopier {
	t.Helper()
	c := testBackend()
	c.Endpoint = endpoint
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	return p.(ObjectSnapshotCopier)
}

func snapshotCopyTestItem() ObjectVersion {
	return ObjectVersion{Key: "key", VersionID: "v1", ETag: `"source"`, Size: 4, LastModified: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
}

func TestS3SnapshotCopyAdoptsAfterUncertainAcknowledgment(t *testing.T) {
	for _, failure := range []string{"none", "acknowledgment", "readback", "verification"} {
		t.Run(failure, func(t *testing.T) {
			native := &snapshotNativeCopyFixture{failure: failure}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }))
			t.Cleanup(server.Close)
			_, err := CopyAndVerifyObjectVersion(t.Context(), snapshotCopyTestProvider(t, server.URL), "source", "destination", snapshotCopyTestItem())
			uncertain := failure == "readback" || failure == "verification"
			if uncertain && !errors.Is(err, ErrUnavailable) || !uncertain && err != nil {
				t.Fatal("first attempt", err)
			}
			// Reconstruct the driver just as a process replacing a worker whose
			// durable CopiedAt checkpoint was lost must do.
			p := snapshotCopyTestProvider(t, server.URL)
			copy, err := CopyAndVerifyObjectVersion(t.Context(), p, "source", "destination", snapshotCopyTestItem())
			if err != nil || len(copy.SHA256) != 64 || copy.ProviderVersionID != "target-v1" {
				t.Fatal("recovered copy", copy, err)
			}
			native.mu.Lock()
			copies, attempts := native.copies, native.attempts
			native.mu.Unlock()
			if copies != 1 || attempts != 1 {
				t.Fatal("copy replay retained extra versions", copies, attempts)
			}
			other := snapshotCopyTestItem()
			other.VersionID = "another-source-version"
			if _, err = CopyAndVerifyObjectVersion(t.Context(), p, "source", "destination", other); !errors.Is(err, ErrConflict) {
				t.Fatal("recovery adopted a different source manifest", err)
			}
		})
	}
}

func TestS3SnapshotConcurrentCopiesCreateOneVersion(t *testing.T) {
	native := &snapshotNativeCopyFixture{concurrentMissingReadBarrier: make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }))
	t.Cleanup(server.Close)
	results := make(chan error, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 2 {
		provider := snapshotCopyTestProvider(t, server.URL)
		wg.Go(func() {
			_, err := CopyAndVerifyObjectVersion(ctx, provider, "source", "destination", snapshotCopyTestItem())
			results <- err
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	if native.copies != 1 || native.attempts != 2 {
		t.Fatal("concurrent snapshot copies retained extra versions", native.copies, native.attempts)
	}
}

func TestS3SnapshotCopyRefusesUnownedDestination(t *testing.T) {
	native := &snapshotNativeCopyFixture{copies: 1, receipt: "another-copy"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }))
	t.Cleanup(server.Close)
	_, err := CopyAndVerifyObjectVersion(t.Context(), snapshotCopyTestProvider(t, server.URL), "source", "destination", snapshotCopyTestItem())
	native.mu.Lock()
	defer native.mu.Unlock()
	if !errors.Is(err, ErrConflict) || native.attempts != 0 || native.copies != 1 {
		t.Fatal("snapshot copy adopted or overwrote an unrelated object", err, native.attempts, native.copies)
	}
}

func TestS3RawCopyDoesNotRetryUncertainMutation(t *testing.T) {
	var requests atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
	}))
	t.Cleanup(native.Close)
	_, err := snapshotCopyTestProvider(t, native.URL).CopyObjectBetweenBuckets(t.Context(), "source", "destination", CopyObjectRequest{SourceKey: "key", SourceVersion: "v1", DestinationKey: "key"})
	if !errors.Is(err, ErrUnavailable) || requests.Load() != 1 {
		t.Fatal("uncertain raw copy was replayed", err, requests.Load())
	}
}

func TestS3SnapshotSizeGuardPrecedesNativeRequests(t *testing.T) {
	var requests atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(native.Close)
	p := snapshotCopyTestProvider(t, native.URL)
	item := snapshotCopyTestItem()
	item.Size = api.MaxObjectSinglePutBytes + 1
	_, err := CopyAndVerifyObjectVersion(t.Context(), p, "source", "destination", item)
	var size *SnapshotCopySizeError
	if !errors.As(err, &size) || size.SizeBytes != item.Size || size.LimitBytes != api.MaxObjectSinglePutBytes || !errors.Is(err, ErrUnsupported) || requests.Load() != 0 {
		t.Fatal("oversized snapshot object reached native copy", err, requests.Load())
	}
	item.Size = api.MaxObjectSinglePutBytes
	if err := p.(SnapshotCopyValidator).ValidateSnapshotCopy(item); err != nil {
		t.Fatal("native copy ceiling was refused", err)
	}
}
