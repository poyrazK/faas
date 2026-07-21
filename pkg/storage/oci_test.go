package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/onebox-faas/faas/pkg/oci"
)

// fakeRegistry is the in-process OCI distribution-spec v2 server that
// pkg/storage/oci_test.go exercises. It implements just enough of the
// registry API for OCIRegistryStorageBackend.Put/Get/Delete/List to
// round-trip:
//
//	GET  /token                                    → anonymous bearer
//	POST /v2/<repo>/blobs/uploads/?digest=sha256:… → monolithic blob PUT (202+Location by default;
//	                                                   opt-in via monolithicOK=true to also accept 201)
//	PUT  /v2/<repo>/blobs/uploads/<uuid>?digest=…  → chunked upload follow-up (registered by POST 202)
//	PUT  /v2/<repo>/manifests/<digest>             → store manifest (DELETE is by digest per spec)
//	GET  /v2/<repo>/manifests/<tag-or-digest>      → return stored manifest
//	DELETE /v2/<repo>/manifests/<tag>              → 405 (spec: DELETE must be by digest)
//	DELETE /v2/<repo>/manifests/<digest>           → remove
//	GET  /v2/<repo>/blobs/<digest>                 → return stored blob
//	GET  /v2/<repo>/tags/list                      → return stored tags
//
// It mirrors pkg/oci/registry_test.go's fakeRegistry shape but
// covers the push side (POST + PUT) and tag listing that the puller
// doesn't exercise.
type fakeRegistry struct {
	srv          *httptest.Server
	token        string
	requireToken bool
	// monolithicOK: when true the blob upload POST returns 201 directly.
	// When false (the default) it returns 202 + Location so the
	// driver's fallback PUT path is exercised — production registries
	// vary in which they return.
	monolithicOK bool
	mu           sync.Mutex
	// blobs: digest → body
	blobs map[string][]byte
	// manifests: repo → tag OR digest → raw JSON body
	manifests map[string]map[string][]byte
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{
		token:        "tok-abc",
		requireToken: true,
		monolithicOK: false, // default: 202+Location, exercises the PUT fallback
		blobs:        map[string][]byte{},
		manifests:    map[string]map[string][]byte{},
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("scope") == "" {
			http.Error(w, "missing scope", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":%q}`, f.token)
	})

	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v2/")
		switch {
		case strings.Contains(path, "/blobs/uploads/"):
			if f.requireToken && r.Header.Get("Authorization") != "Bearer "+f.token {
				authChallenge(w, f.srv.URL, "repository:test:pull,push")
				return
			}
			// Two-step chunked upload: PUT to /v2/<repo>/blobs/uploads/<uuid>
			// completes a blob the registry advertised via a 202 Location.
			// Body and digest verification mirror the monolithic POST.
			if strings.Contains(path, "/blobs/uploads/") && r.Method == http.MethodPut {
				digest := r.URL.Query().Get("digest")
				if !strings.HasPrefix(digest, "sha256:") {
					http.Error(w, "missing digest", http.StatusBadRequest)
					return
				}
				body, err := io.ReadAll(io.LimitReader(r.Body, 4<<30))
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				sum := sha256.Sum256(body)
				got := "sha256:" + hex.EncodeToString(sum[:])
				if got != digest {
					http.Error(w, fmt.Sprintf("digest mismatch: want %s got %s", digest, got), http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.blobs[digest] = body
				f.mu.Unlock()
				w.Header().Set("Docker-Content-Digest", digest)
				w.WriteHeader(http.StatusCreated)
				return
			}
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			digest := r.URL.Query().Get("digest")
			if !strings.HasPrefix(digest, "sha256:") {
				http.Error(w, "missing digest", http.StatusBadRequest)
				return
			}
			if f.monolithicOK {
				body, err := io.ReadAll(io.LimitReader(r.Body, 4<<30))
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				sum := sha256.Sum256(body)
				got := "sha256:" + hex.EncodeToString(sum[:])
				if got != digest {
					http.Error(w, fmt.Sprintf("digest mismatch: want %s got %s", digest, got), http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.blobs[digest] = body
				f.mu.Unlock()
				w.Header().Set("Docker-Content-Digest", digest)
				w.WriteHeader(http.StatusCreated)
				return
			}
			// 202 + Location: the registry wants the caller to PUT the
			// body. Use a stable per-request UUID so the Location header
			// is a path the same mux can dispatch (no second handler
			// registration; the /v2/ switch above also matches PUTs).
			uploadID := fmt.Sprintf("upload-%d", time.Now().UnixNano())
			w.Header().Set("Location", fmt.Sprintf("%s/v2/%s/blobs/uploads/%s", f.srv.URL, extractRepoFromUploadPath(path), uploadID))
			w.WriteHeader(http.StatusAccepted)
		case strings.Contains(path, "/manifests/"):
			parts := strings.SplitN(path, "/manifests/", 2)
			if len(parts) != 2 {
				http.NotFound(w, r)
				return
			}
			repo, ref := parts[0], parts[1]
			if f.requireToken && r.Header.Get("Authorization") != "Bearer "+f.token {
				authChallenge(w, f.srv.URL, "repository:test:pull,push")
				return
			}
			switch r.Method {
			case http.MethodPut:
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				f.mu.Lock()
				if f.manifests[repo] == nil {
					f.manifests[repo] = map[string][]byte{}
				}
				// Accept under both the supplied reference (tag OR digest)
				// so the driver can PUT by tag and later DELETE by digest.
				f.manifests[repo][ref] = body
				f.mu.Unlock()
				w.Header().Set("Docker-Content-Digest", digestOf(body))
				w.WriteHeader(http.StatusCreated)
			case http.MethodGet:
				f.mu.Lock()
				body, ok := f.manifests[repo][ref]
				f.mu.Unlock()
				if !ok {
					http.Error(w, "manifest not found", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
				w.Header().Set("Docker-Content-Digest", digestOf(body))
				_, _ = w.Write(body)
			case http.MethodDelete:
				// Spec: manifest DELETE must be by digest, not tag.
				// A reference that doesn't look like sha256:<hex> gets
				// 405 so a regression to tag-DELETE surfaces as a test
				// failure rather than silent success.
				if !strings.HasPrefix(ref, "sha256:") {
					http.Error(w, "manifest DELETE must reference a digest", http.StatusMethodNotAllowed)
					return
				}
				f.mu.Lock()
				delete(f.manifests[repo], ref)
				f.mu.Unlock()
				w.WriteHeader(http.StatusAccepted)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		case strings.Contains(path, "/blobs/"):
			parts := strings.SplitN(path, "/blobs/", 2)
			if len(parts) != 2 {
				http.NotFound(w, r)
				return
			}
			digest := parts[1]
			if f.requireToken && r.Header.Get("Authorization") != "Bearer "+f.token {
				authChallenge(w, f.srv.URL, "repository:test:pull")
				return
			}
			f.mu.Lock()
			body, ok := f.blobs[digest]
			f.mu.Unlock()
			if !ok {
				http.Error(w, "blob not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.layer.v1.tar+gzip")
			w.Header().Set("Docker-Content-Digest", digest)
			_, _ = w.Write(body)
		case strings.HasSuffix(path, "/tags/list"):
			repo := strings.TrimSuffix(path, "/tags/list")
			if f.requireToken && r.Header.Get("Authorization") != "Bearer "+f.token {
				authChallenge(w, f.srv.URL, "repository:test:pull")
				return
			}
			f.mu.Lock()
			tags := make([]string, 0, len(f.manifests[repo]))
			for tag := range f.manifests[repo] {
				tags = append(tags, tag)
			}
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": repo,
				"tags": tags,
			})
		default:
			http.NotFound(w, r)
		}
	})

	f.srv = httptest.NewServer(mux)
	return f
}

// authChallenge writes the spec-compliant 401 + WWW-Authenticate so
// the OCI driver's bearer-dance path can be exercised. Inline because
// httptest.NewServer's URL isn't known at registration time.
func authChallenge(w http.ResponseWriter, srvURL, scope string) {
	w.Header().Set("Www-Authenticate",
		fmt.Sprintf(`Bearer realm=%q,service="registry",scope=%q`, srvURL+"/token", scope))
	w.WriteHeader(http.StatusUnauthorized)
}

func (f *fakeRegistry) client(t *testing.T) *OCIRegistryStorageBackend {
	t.Helper()
	o, err := NewOCIRegistryStorageBackend(
		WithRegistry(f.srv.URL), // include scheme (http://) — production passes https://
		WithHTTPClient(f.srv.Client()),
		WithRepoPrefix("faas"),
	)
	if err != nil {
		t.Fatalf("NewOCIRegistryStorageBackend: %v", err)
	}
	return o
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// extractRepoFromUploadPath pulls "<repo>" out of a path like
// "faas/apps/blobs/uploads/" — the segment before "/blobs/uploads/".
// The fake uses it to build the absolute Location URL it returns on
// the 202 Accepted branch.
func extractRepoFromUploadPath(path string) string {
	const sep = "/blobs/uploads/"
	if idx := strings.Index(path, sep); idx >= 0 {
		return path[:idx]
	}
	return ""
}

// --- plan() tests ------------------------------------------------------

func TestPlan_AppsKey(t *testing.T) {
	o := &OCIRegistryStorageBackend{prefix: "faas"}
	depUUID := "550e8400-e29b-41d4-a716-446655440000"
	repo, tag, err := o.plan("apps/my-app/" + depUUID + ".ext4")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if repo != "apps" {
		t.Errorf("repo = %q, want apps", repo)
	}
	if tag != "my-app__"+depUUID {
		t.Errorf("tag = %q, want my-app__%s", tag, depUUID)
	}
}

func TestPlan_SnapKey(t *testing.T) {
	o := &OCIRegistryStorageBackend{prefix: "faas"}
	depUUID := "550e8400-e29b-41d4-a716-446655440000"
	repo, tag, err := o.plan("snap/" + depUUID + "/mem")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if repo != "snap-"+depUUID {
		t.Errorf("repo = %q, want snap-%s", repo, depUUID)
	}
	if tag != "mem" {
		t.Errorf("tag = %q, want mem", tag)
	}
}

func TestPlan_LayersKey(t *testing.T) {
	o := &OCIRegistryStorageBackend{prefix: "faas"}
	depUUID := "550e8400-e29b-41d4-a716-446655440000"
	repo, tag, err := o.plan("layers/" + depUUID + ".ext4")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if repo != "layers" || tag != depUUID {
		t.Errorf("got (%q,%q), want (layers, %s)", repo, tag, depUUID)
	}
}

func TestPlan_BaseKey(t *testing.T) {
	o := &OCIRegistryStorageBackend{prefix: "faas"}
	repo, tag, err := o.plan("base/runner-node22.ext4")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if repo != "base" {
		t.Errorf("repo = %q, want base", repo)
	}
	if tag != "runner-node22" {
		t.Errorf("tag = %q, want runner-node22", tag)
	}
}

func TestPlan_BaseDigestKey(t *testing.T) {
	o := &OCIRegistryStorageBackend{prefix: "faas"}
	repo, tag, err := o.plan("base/runner-node22.ext4.digest")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if repo != "base" || tag != "runner-node22-digest" {
		t.Errorf("got (%q,%q), want (base, runner-node22-digest)", repo, tag)
	}
}

func TestPlan_KernelKey(t *testing.T) {
	o := &OCIRegistryStorageBackend{prefix: "faas"}
	repo, tag, err := o.plan("kernel/v1.10.0")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if repo != "kernel" || tag != "v1.10.0" {
		t.Errorf("got (%q,%q), want (kernel, v1.10.0)", repo, tag)
	}
}

func TestPlan_InvalidKeys(t *testing.T) {
	o := &OCIRegistryStorageBackend{prefix: "faas"}
	tests := []string{
		"",                     // empty
		"apps/",                // apps without slug
		"apps/slug/",           // apps without dep
		"apps/slug/dep",        // missing .ext4
		"apps/slug/dep.tar.gz", // wrong extension
		"snap/abcd1234/mem",    // bare-hex dep no longer accepted (UUID only)
		"snap/550e8400-e29b-41d4-a716-44665544000z/mem",   // non-hex UUID char
		"snap/550e8400-e29b-41d4-a716-446655440000/bogus", // wrong segment
		"base/runner.ext4.digest.digest",                  // double-suffix
		"layers/abc.txt",                                  // wrong extension
		"unknown/foo/bar",                                 // unknown namespace
		"apps/slug!/dep.ext4",                             // bang is not in tag charset
	}
	for _, k := range tests {
		t.Run(k, func(t *testing.T) {
			_, _, err := o.plan(k)
			if err == nil {
				t.Errorf("plan(%q): expected error, got nil", k)
			}
			if !IsInvalidKey(err) {
				t.Errorf("plan(%q): error %v does not wrap ErrInvalidKey", k, err)
			}
		})
	}
}

// --- roundtrip tests ---------------------------------------------------

// TestOCIRoundTrip exercises Put/Get for each key shape. Each
// Put/Get uses a small synthetic body so the test stays fast.
func TestOCIRoundTrip(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)

	// Two real UUIDs to exercise the apps-tag separator and the
	// snap-per-deployment repo namespacing under one test run.
	dep1 := "550e8400-e29b-41d4-a716-446655440000"
	dep2 := "660e8400-e29b-41d4-a716-446655440001"
	keys := []string{
		"apps/my-app/" + dep1 + ".ext4",
		"snap/" + dep1 + "/mem",
		"snap/" + dep1 + "/vmstate",
		"base/runner-node22.ext4",
		"base/runner-node22.ext4.digest",
		"layers/" + dep2 + ".ext4",
		"kernel/v1.10.0",
	}
	bodies := [][]byte{
		[]byte("fake app layer bytes - 130 MB scaled down"),
		[]byte("snapshot mem blob"),
		[]byte("snapshot vmstate blob"),
		[]byte("base ext4 bytes"),
		[]byte("sha256:0000000000000000000000000000000000000000000000000000000000000000"),
		[]byte("legacy layer bytes"),
		[]byte("firecracker kernel bytes"),
	}

	for i, key := range keys {
		t.Run(key, func(t *testing.T) {
			ctx := context.Background()
			body := bodies[i]
			if err := be.Put(ctx, key, bytes.NewReader(body)); err != nil {
				t.Fatalf("Put(%q): %v", key, err)
			}
			rc, err := be.Get(ctx, key)
			if err != nil {
				t.Fatalf("Get(%q): %v", key, err)
			}
			got, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatalf("Get(%q): read: %v", key, err)
			}
			if !bytes.Equal(got, body) {
				t.Errorf("Get(%q): round-trip mismatch\nwant %q\ngot  %q", key, body, got)
			}
		})
	}
}

// TestOCIGetMissing covers the cold-boot-fallback path (ADR-005):
// Get on a key that was never Put must return ErrNotFound.
func TestOCIGetMissing(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	_, err := be.Get(context.Background(), "apps/my-app/00000000-0000-4000-8000-000000000000.ext4")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if !IsNotFound(err) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestOCIDeleteIdempotent covers the LocalStorageBackend-style
// idempotent Delete: second Delete is a no-op. The driver MUST
// resolve the manifest digest before DELETE; the fake rejects tag-
// based DELETE so a regression to that path is loud.
func TestOCIDeleteIdempotent(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	ctx := context.Background()
	depUUID := "550e8400-e29b-41d4-a716-446655440000"
	key := "apps/my-app/" + depUUID + ".ext4"
	if err := be.Put(ctx, key, bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := be.Delete(ctx, key); err != nil {
		t.Fatalf("first Delete: %v", err)
	}
	if err := be.Delete(ctx, key); err != nil {
		t.Errorf("second Delete should be no-op, got %v", err)
	}
}

// TestOCIListUnderApps pushes two apps under the same repo and asserts
// List("apps/") returns both. The snap/ List path is exercised in a
// separate test because it relies on the knownRepos cache.
func TestOCIListUnderApps(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	ctx := context.Background()
	dep1 := "550e8400-e29b-41d4-a716-446655440000"
	dep2 := "660e8400-e29b-41d4-a716-446655440001"
	if err := be.Put(ctx, "apps/foo/"+dep1+".ext4", bytes.NewReader([]byte("a"))); err != nil {
		t.Fatalf("Put foo: %v", err)
	}
	if err := be.Put(ctx, "apps/bar/"+dep2+".ext4", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("Put bar: %v", err)
	}
	got, err := be.List(ctx, "apps/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d keys, want 2: %v", len(got), got)
	}
	want := map[string]bool{
		"apps/foo/" + dep1 + ".ext4": true,
		"apps/bar/" + dep2 + ".ext4": true,
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("List returned unexpected key %q", k)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("List missing keys: %v", want)
	}
}

// TestOCIListUnderSnap exercises the snap/ fan-out via knownRepos.
// We pre-warm knownRepos by issuing Puts; on a cold start without a
// populated knownRepos the list would be empty (the registry's
// /v2/_catalog is not implemented by most public registries).
func TestOCIListUnderSnap(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	ctx := context.Background()
	depUUID := "550e8400-e29b-41d4-a716-446655440000"
	if err := be.Put(ctx, "snap/"+depUUID+"/mem", bytes.NewReader([]byte("m"))); err != nil {
		t.Fatalf("Put snap mem: %v", err)
	}
	if err := be.Put(ctx, "snap/"+depUUID+"/vmstate", bytes.NewReader([]byte("v"))); err != nil {
		t.Fatalf("Put snap vmstate: %v", err)
	}
	got, err := be.List(ctx, "snap/")
	if err != nil {
		t.Fatalf("List snap: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List snap returned %d keys, want 2: %v", len(got), got)
	}
	want := map[string]bool{
		"snap/" + depUUID + "/mem":     true,
		"snap/" + depUUID + "/vmstate": true,
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("List returned unexpected key %q", k)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("List missing keys: %v", want)
	}
}

// TestOCIListEmptyReturnsNoError covers the empty-precondition: an
// unknown prefix yields an empty slice, not an error.
func TestOCIListEmptyReturnsNoError(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	got, err := be.List(context.Background(), "unknown-prefix/")
	if err != nil {
		t.Fatalf("List unknown-prefix: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List unknown-prefix returned %d keys, want 0: %v", len(got), got)
	}
}

// TestOCIRequiresRegistry verifies the constructor's empty-registry
// rejection — silent default would publish into the wrong namespace.
func TestOCIRequiresRegistry(t *testing.T) {
	_, err := NewOCIRegistryStorageBackend()
	if err == nil {
		t.Fatal("expected error for empty registry")
	}
	if !IsInvalidKey(err) {
		t.Errorf("error %v does not wrap ErrInvalidKey", err)
	}
}

// TestOCIPutInvalidKey covers the validateKey-rejection-at-Put path.
func TestOCIPutInvalidKey(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	for _, k := range []string{"", "..", "apps/foo", "/abs/key"} {
		t.Run(k, func(t *testing.T) {
			err := be.Put(context.Background(), k, bytes.NewReader([]byte("x")))
			if !IsInvalidKey(err) {
				t.Errorf("Put(%q): expected ErrInvalidKey, got %v", k, err)
			}
		})
	}
}

// TestOCIGetInvalidKey covers the validateKey-rejection-at-Get path.
func TestOCIGetInvalidKey(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	for _, k := range []string{"", "..", "apps/foo", "/abs/key"} {
		t.Run(k, func(t *testing.T) {
			_, err := be.Get(context.Background(), k)
			if !IsInvalidKey(err) {
				t.Errorf("Get(%q): expected ErrInvalidKey, got %v", k, err)
			}
		})
	}
}

// TestOCIContextCancellation verifies that a cancelled ctx surfaces
// ctx.Err() out of Put without leaving the registry holding a
// partial upload. The registry spy asserts the upload never reaches
// it (the body never arrives because the request is aborted before
// the handler reads).
func TestOCIContextCancellation(t *testing.T) {
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"token":"t"}`)
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		// Never read the body; just consume the request and return 5xx.
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	be, err := NewOCIRegistryStorageBackend(
		WithRegistry(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatalf("NewOCIRegistryStorageBackend: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE Put → should error out before any network IO
	err = be.Put(ctx, "apps/foo/550e8400-e29b-41d4-a716-446655440000.ext4", bytes.NewReader([]byte("payload")))
	if err == nil {
		t.Fatal("expected error from cancelled ctx, got nil")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context-cancelled error, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("cancelled Put made %d network calls, want 0", got)
	}
}

// TestOCIWithCustomPrefix verifies the WithRepoPrefix option. An
// operator who already has a "faas" repo on a shared registry
// overrides the prefix to avoid collisions.
func TestOCIWithCustomPrefix(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be, err := NewOCIRegistryStorageBackend(
		WithRegistry(f.srv.URL),
		WithHTTPClient(f.srv.Client()),
		WithRepoPrefix("faas-team-a"),
	)
	if err != nil {
		t.Fatalf("NewOCIRegistryStorageBackend: %v", err)
	}
	if be.RepoPrefix() != "faas-team-a" {
		t.Errorf("RepoPrefix = %q, want faas-team-a", be.RepoPrefix())
	}
	ctx := context.Background()
	depUUID := "550e8400-e29b-41d4-a716-446655440000"
	key := "apps/foo/" + depUUID + ".ext4"
	if err := be.Put(ctx, key, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	f.mu.Lock()
	_, ok := f.manifests["faas-team-a/apps"]["foo__"+depUUID]
	f.mu.Unlock()
	if !ok {
		t.Errorf("manifest not stored under faas-team-a/apps/foo__%s", depUUID)
	}
}

// TestOCIUnplanInverse verifies plan() / unplan() are inverses for
// every supported key shape. Round-tripping a key through both
// functions must produce the original.
func TestOCIUnplanInverse(t *testing.T) {
	o := &OCIRegistryStorageBackend{prefix: "faas"}
	depUUID := "550e8400-e29b-41d4-a716-446655440000"
	keys := []string{
		"apps/slug-1/" + depUUID + ".ext4",
		"snap/" + depUUID + "/mem",
		"snap/" + depUUID + "/vmstate",
		"base/runner-node22.ext4",
		"base/runner-node22.ext4.digest",
		"layers/" + depUUID + ".ext4",
		"kernel/v1.10.0",
	}
	for _, k := range keys {
		t.Run(k, func(t *testing.T) {
			repo, tag, err := o.plan(k)
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			got, ok := o.unplan(repo, tag)
			if !ok {
				t.Fatalf("unplan(%q,%q): not recognised", repo, tag)
			}
			if got != k {
				t.Errorf("plan/unplan roundtrip: %q → %q → %q", k, repo+"/"+tag, got)
			}
		})
	}
}

// TestOCILargeBlobStreaming exercises the streaming upload path: a
// 64 MiB body has to be hashed AND shipped end-to-end without
// buffering. Hash mismatch on the registry side is a structural
// bug in the SHA-256 tee (covered by the fake's POST check).
func TestOCILargeBlobStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 64 MiB stream test in -short mode")
	}
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)

	body := make([]byte, 64<<20) // 64 MiB
	for i := range body {
		body[i] = byte(i % 251)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	depUUID := "550e8400-e29b-41d4-a716-446655440000"
	if err := be.Put(ctx, "apps/foo/"+depUUID+".ext4", bytes.NewReader(body)); err != nil {
		t.Fatalf("Put 64MiB: %v", err)
	}
	rc, err := be.Get(ctx, "apps/foo/"+depUUID+".ext4")
	if err != nil {
		t.Fatalf("Get 64MiB: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("Get read: %v", err)
	}
	if len(got) != len(body) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(body))
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("64 MiB body mismatch (sha256 catch-all)")
	}
}

// TestOCIEgressComposition asserts the storage backend defaults to the
// RFC1918/loopback/link-local-denying egress transport when
// WithHTTPClient is NOT passed. The composition is security-critical —
// a misconfigured FAAS_OCI_REGISTRY pointing at a private host must
// refuse to dial, same posture as the build-time puller (spec §11).
//
// Without this test a future constructor change could silently replace
// the egress-guarded client with a vanilla http.Client while all
// underlying pkg/oci egress tests stay green.
func TestOCIEgressComposition(t *testing.T) {
	be, err := NewOCIRegistryStorageBackend(
		WithRegistry("https://10.0.0.5"), // RFC1918 — must be refused
	)
	if err != nil {
		t.Fatalf("NewOCIRegistryStorageBackend: %v", err)
	}
	err = be.Put(context.Background(), "apps/foo/550e8400-e29b-41d4-a716-446655440000.ext4",
		bytes.NewReader([]byte("x")))
	if err == nil {
		t.Fatal("Put to RFC1918 host should have been refused by the egress transport, got nil")
	}
	if !errors.Is(err, oci.ErrEgressDenied) {
		t.Errorf("err = %v, want errors.Is(_, oci.ErrEgressDenied) true", err)
	}
}

// TestOCIBearerRealmPin asserts the bearer-realm trust boundary
// described on bearerForChallenge. With FAAS_OCI_USERNAME set, a
// 401 challenge advertising a cleartext or unrelated-host realm is
// rejected before any token-endpoint request goes out.
func TestOCIBearerRealmPin(t *testing.T) {
	tests := []struct {
		name       string
		realm      string
		wantErrSub string
	}{
		{
			name:       "cleartext realm rejected",
			realm:      "http://ghcr.io/token",
			wantErrSub: "must be https",
		},
		{
			name:       "unrelated-host realm rejected",
			realm:      "https://attacker.example.com/token",
			wantErrSub: "is not the registry host",
		},
		{
			name:       "single-segment subdomain allowed",
			realm:      "https://auth.ghcr.io/token",
			wantErrSub: "", // accepted — go past the realm guard, fail on the
			// token-endpoint dial against an unreachable host (the test
			// httptest fake serves only the registry host).
		},
		{
			name:       "parent-host realm allowed",
			realm:      "https://ghcr.io/token",
			wantErrSub: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			be, err := NewOCIRegistryStorageBackend(
				WithRegistry("https://ghcr.io/onebox"),
				WithCredentials("user", "pw"),
				WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
			)
			if err != nil {
				t.Fatalf("NewOCIRegistryStorageBackend: %v", err)
			}
			// Probe the bearer challenge path directly — no registry
			// needed because validateBearerRealm is a pure-function
			// gate. We test through the public surface anyway for
			// parity with how the production 401 path exercises it.
			_, err = be.bearerForChallenge(context.Background(),
				fmt.Sprintf(`Bearer realm=%q,service="registry"`, tc.realm),
				"repository:faas/apps:pull")
			if tc.wantErrSub == "" {
				// Expect a network/dial error (or a 401 from the
				// in-process fake), not a realm-guard error.
				if err != nil && strings.Contains(err.Error(), "realm") {
					t.Errorf("expected realm guard to accept, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected realm guard to reject %q, got nil", tc.realm)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("err = %v, want substring %q", err, tc.wantErrSub)
			}
		})
	}
}

// TestOCIMonolithicOK exercises the 201-Created blob upload variant.
// Some registries accept the Single POST flow outright (201 Created)
// instead of the 202+Location dance the fake default exercises.
// Pinned here so both branches are regression-tested.
func TestOCIMonolithicOK(t *testing.T) {
	f := newFakeRegistry(t)
	f.monolithicOK = true
	defer f.srv.Close()
	be := f.client(t)
	depUUID := "550e8400-e29b-41d4-a716-446655440000"
	key := "apps/foo/" + depUUID + ".ext4"
	if err := be.Put(context.Background(), key, bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("Put (monolithic 201): %v", err)
	}
	rc, err := be.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("Get read: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("Get bytes = %q, want %q", got, "payload")
	}
}

// TestOCIRequiresRepoForEmptyRegistry remains the constructor-time
// guard against silent cross-org publishes (see NewOCIRegistryStorageBackend).
