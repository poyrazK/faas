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

// TestOCIGetVerifiesBlobIntegrity covers the Get-path SHA-256 check:
// the manifest advertises a layer digest; the driver MUST confirm the
// downloaded body matches that digest before returning it. A corrupted
// body (registry served a different blob than the manifest named) must
// surface as an error, not silently return garbage to the caller.
func TestOCIGetVerifiesBlobIntegrity(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	ctx := context.Background()

	depUUID := "550e8400-e29b-41d4-a716-446655440000"
	original := []byte("the quick brown fox jumps over the lazy dog")
	if err := be.Put(ctx, "apps/foo/"+depUUID+".ext4", bytes.NewReader(original)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Corrupt the stored blob behind the fake's back. The manifest
	// still references the original sha256, but the body now has a
	// different hash. The driver's Get must detect the mismatch.
	f.mu.Lock()
	for k, v := range f.blobs {
		corrupt := append([]byte(nil), v...)
		corrupt[0] ^= 0xFF // flip a bit
		f.blobs[k] = corrupt
	}
	f.mu.Unlock()

	_, err := be.Get(ctx, "apps/foo/"+depUUID+".ext4")
	if err == nil {
		t.Fatal("Get with corrupt blob: want error, got nil")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") &&
		!strings.Contains(err.Error(), "integrity") {
		t.Errorf("Get error %q does not mention integrity failure", err)
	}
}

// TestOCIGetRejectsNonSha256Digest covers the defensive branch in
// fetchBlob: a manifest whose layer digest doesn't follow the
// "sha256:<hex>" format is rejected without buffering the body.
// This guards against a registry (or attacker) handing us a manifest
// with an unexpected digest algorithm we can't verify.
func TestOCIGetRejectsNonSha256Digest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"tok"}`)
	})
	var srv *httptest.Server
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate",
				`Bearer realm="`+srv.URL+`/token",service="x",scope="repository:x:pull,push"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			// Craft a manifest whose layer digest is NOT sha256:.
			// sha512 is a valid OCI digest algorithm (the spec is
			// algorithm-agnostic) but our driver explicitly verifies
			// only sha256 — anything else gets a fail-fast rejection.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:` + strings.Repeat("0", 64) + `","size":7023},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha512:` + strings.Repeat("0", 128) + `","size":12}]}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	be, err := NewOCIRegistryStorageBackend(
		WithRegistry(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatalf("NewOCIRegistryStorageBackend: %v", err)
	}
	_, err = be.Get(context.Background(), "apps/foo/550e8400-e29b-41d4-a716-446655440000.ext4")
	if err == nil {
		t.Fatal("Get with sha512 digest: want error, got nil")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Errorf("Get error %q does not mention the expected sha256 algorithm", err)
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

// TestOCIListSurfacesSingleRepoError verifies that a single-repo prefix
// (apps/) propagates the fetchTags failure to the caller. A GC walk
// against a flaky registry must NOT silently return empty; the caller
// decides whether to retry, alert, or skip.
func TestOCIListSurfacesSingleRepoError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"tok"}`)
	})
	var srv *httptest.Server
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate",
				`Bearer realm="`+srv.URL+`/token",service="x",scope="repository:x:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			http.Error(w, "registry on fire", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	be, err := NewOCIRegistryStorageBackend(
		WithRegistry(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatalf("NewOCIRegistryStorageBackend: %v", err)
	}
	got, err := be.List(context.Background(), "apps/")
	if err == nil {
		t.Fatalf("List apps/ with broken registry: want error, got nil (keys=%v)", got)
	}
	if !strings.Contains(err.Error(), "registry on fire") {
		t.Errorf("List error %q does not mention the upstream failure", err)
	}
}

// TestOCIListFanOutContinuesOnPartialFailure verifies the snap/ fan-out
// keeps iterating when one repo fails but another succeeds. The
// returned error is nil because at least one repo enumerated keys.
// Without this, a single flaky snap repo would abort GC across all
// deployments parked on the same host.
func TestOCIListFanOutContinuesOnPartialFailure(t *testing.T) {
	const (
		goodRepo = "snap-good"
		badRepo  = "snap-bad"
	)
	// The fan-out test doesn't need a real Put — knownRepos is the
	// only state that drives which repos List walks. Inject the
	// per-repo tags/list overrides directly on the fake so the test
	// stays focused on List's error-surfacing behaviour.
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	overrideTagsListForFake(t, f, map[string]fakeTagsListReply{
		badRepo:  {status: http.StatusInternalServerError, body: "down"},
		goodRepo: {status: http.StatusOK, body: `{"name":"snap-good","tags":["mem"]}`},
	})
	manipulateKnownReposForTest(be, []string{badRepo, goodRepo})

	got, err := be.List(context.Background(), "snap/")
	if err == nil {
		t.Fatalf("List snap/ with one bad repo: want non-nil error (partial failure surfaces the bad repo), got nil (keys=%v)", got)
	}
	if !strings.Contains(err.Error(), "down") {
		t.Errorf("List error %q does not mention the bad repo's failure", err)
	}
	foundMem := false
	for _, k := range got {
		if k == "snap/good/mem" {
			foundMem = true
			break
		}
	}
	if !foundMem {
		t.Errorf("expected snap/good/mem from good repo in %v", got)
	}
}

// fakeTagsListReply is the per-repo reply injected by
// overrideTagsListForFake. Status=0 means "fall through to the
// underlying fake" (useful when only some repos need a forced reply).
type fakeTagsListReply struct {
	status int
	body   string
}

// overrideTagsListForFake installs a hook on the fake registry's mux
// so a specific subset of repos get a forced tags/list reply. Any
// repo not in the map falls through to the fake's manifest-driven
// behaviour (returning the empty tags slice for repos with no
// pushed manifest, exactly like a real registry on a cold GC walk).
//
// The hook is implemented by wrapping the fake's existing handler
// chain: the override wins for the configured repos, and the rest
// is delegated unchanged.
func overrideTagsListForFake(t *testing.T, f *fakeRegistry, replies map[string]fakeTagsListReply) {
	t.Helper()
	// Replace the fake's mux on the live server: cancel the old
	// handler and mount a fresh mux that combines auth + Put plumbing
	// with the override dispatch. To avoid rebuilding the full
	// fakeRegistry from scratch, we leverage http.ServeMux's
	// most-specific-path-wins semantics: register the override paths
	// FIRST so they match before the catch-all `/v2/`.
	mux := http.NewServeMux()
	// Re-register auth + blob upload + manifest push by reusing the
	// fake's existing maps. Easiest: copy the existing handler logic
	// verbatim. To keep this test seam small we delegate to the
	// original mux via a closure over the same handler.
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v2/")
		if strings.HasSuffix(path, "/tags/list") {
			// path is "<prefix>/<repo>/tags/list"; strip suffix and the
			// prefix segment to get the bare repo name. The override
			// map is keyed by the bare name to mirror what the test
			// caller knows.
			repoFull := strings.TrimSuffix(path, "/tags/list")
			repoBare := repoFull
			if idx := strings.Index(repoFull, "/"); idx >= 0 {
				repoBare = repoFull[idx+1:]
			}
			if reply, ok := replies[repoBare]; ok && reply.status != 0 {
				if reply.status >= 400 {
					http.Error(w, reply.body, reply.status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(reply.body))
				return
			}
		}
		// Fall through to the existing fake's /v2/ handler via a
		// fresh mux registration that mirrors the production fake.
		dispatchV2Fake(w, r, f)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("scope") == "" {
			http.Error(w, "missing scope", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":%q}`, f.token)
	})
	// Swap the server's handler. httptest.Server exposes Handler via
	// the embedded http.Server field.
	f.srv.Config.Handler = mux
}

// dispatchV2Fake re-dispatches a /v2/ request through the fakeRegistry's
// original handler chain. We can't call the original closure directly
// (it was registered inline in newFakeRegistry), so we re-build the
// dispatch inline at this layer. Keep behaviour identical to the
// primary fake — blob upload + manifest push + blob GET + tags/list
// (when not overridden) + manifest GET/DELETE.
func dispatchV2Fake(w http.ResponseWriter, r *http.Request, f *fakeRegistry) {
	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	switch {
	case strings.Contains(path, "/blobs/uploads/"):
		if f.requireToken && r.Header.Get("Authorization") != "Bearer "+f.token {
			authChallenge(w, f.srv.URL, "repository:test:pull,push")
			return
		}
		if r.Method == http.MethodPut {
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
		// 202 + Location (matches the default fake's monolithicOK=false).
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
		_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags})
	default:
		http.NotFound(w, r)
	}
}

// TestOCIListFanOutAllFailingReturnsError verifies that when EVERY
// repo in the fan-out fails, the joined errors surface to the caller.
// A silent empty result would mask a registry outage from GC.
func TestOCIListFanOutAllFailingReturnsError(t *testing.T) {
	// The fan-out test doesn't need a real Put — knownRepos is the
	// only state that drives which repos List walks. Override the
	// tags/list responses on every known repo to fail.
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	overrideTagsListForFake(t, f, map[string]fakeTagsListReply{
		"snap-zzz1": {status: http.StatusServiceUnavailable, body: "registry down"},
		"snap-zzz2": {status: http.StatusInternalServerError, body: "registry down"},
	})
	manipulateKnownReposForTest(be, []string{"snap-zzz1", "snap-zzz2"})

	got, err := be.List(context.Background(), "snap/")
	if err == nil {
		t.Fatalf("List snap/ with all-bad repos: want error, got nil (keys=%v)", got)
	}
	if !strings.Contains(err.Error(), "registry down") {
		t.Errorf("List error %q does not propagate the upstream failure", err)
	}
}

// manipulateKnownReposForTest replaces the in-memory snap-repo list.
// Test-only seam — never called from production. Used by the fan-out
// tests to drive deterministic per-repo behavior. Repos are stored as
// "<prefix>/<repo>" to match the production Put path; the bare repo
// names ("snap-good", "snap-bad") are passed in and the test injects
// them under the configured prefix.
//
// Note: this REPLACES the previous contents (delete + store) — callers
// that want additive behaviour should use Store directly. The
// fan-out tests need the previous Put-populated entries cleared so the
// assertions are deterministic (otherwise a stale Put-time repo
// would still succeed and "all failing" would become "partial").
func manipulateKnownReposForTest(be *OCIRegistryStorageBackend, repos []string) {
	prefix := be.RepoPrefix()
	be.knownRepos.Range(func(k, _ any) bool {
		be.knownRepos.Delete(k)
		return true
	})
	for _, r := range repos {
		be.knownRepos.Store(prefix+"/"+r, true)
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

// TestOCIRefreshGrant exercises the proactive refresh-token flow
// (review finding #6): a near-expiry bearer cached with a refresh_token
// triggers a POST to the realm endpoint on the next request, instead
// of waiting for the registry to 401. The fake serves a token endpoint
// that records refresh calls and returns a fresh bearer.
func TestOCIRefreshGrant(t *testing.T) {
	var tokenHits, refreshHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt32(&refreshHits, 1)
			if err := r.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if r.PostForm.Get("grant_type") != "refresh_token" {
				http.Error(w, "wrong grant", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"token":"refreshed-tok","refresh_token":"rt-2","expires_in":3600}`)
			return
		}
		atomic.AddInt32(&tokenHits, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"initial-tok","refresh_token":"rt-1","expires_in":1}`)
	})
	// Pre-register a /v2/ handler that closes over srv (assigned after
	// NewServer). The Www-Authenticate challenge needs the server URL so
	// the driver's bearerForChallenge can POST to it.
	var srv *httptest.Server
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			authChallenge(w, srv.URL, "repository:faas/apps:pull")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "x", "tags": []string{}})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	be, err := NewOCIRegistryStorageBackend(
		WithRegistry(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatalf("NewOCIRegistryStorageBackend: %v", err)
	}

	// Drive an initial fetch by calling the public surface — fetchTags
	// returns 404 (no manifests) but the bearer dance populates the cache.
	ctx := context.Background()
	if _, err := be.fetchTags(ctx, "apps"); err != nil {
		t.Fatalf("first fetchTags: %v", err)
	}
	if got := atomic.LoadInt32(&tokenHits); got != 1 {
		t.Errorf("initial token endpoint hits = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&refreshHits); got != 0 {
		t.Errorf("unexpected refresh hits after initial fetch: %d", got)
	}

	// Force the cached entry to be near-expiry by rewriting it via the
	// helper. We can't poke issuedAt through the public API, so reach
	// in through a small helper exposed only for tests.
	manipulateCacheForTest(be, "repository:faas/apps:pull", func(ct *cachedToken) {
		ct.tok.ExpiresIn = 1 * time.Nanosecond // forces IsExpiredAt → true
		ct.realmURL = srv.URL + "/token"
	})

	// Second fetchTags should trigger a refresh, not a fresh GET.
	if _, err := be.fetchTags(ctx, "apps"); err != nil {
		t.Fatalf("second fetchTags: %v", err)
	}
	if got := atomic.LoadInt32(&refreshHits); got != 1 {
		t.Errorf("refresh hits = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&tokenHits); got != 1 {
		t.Errorf("token endpoint GET hits = %d, want 1 (refresh path must NOT also do a GET)", got)
	}
}

// manipulateCacheForTest reaches into the package-private tokenCache
// to rewrite a cachedToken entry. Test-only seam — never called from
// production. The closure receives a pointer so the test can mutate
// fields without round-tripping through the public API.
func manipulateCacheForTest(be *OCIRegistryStorageBackend, scope string, mut func(*cachedToken)) {
	v, ok := be.tokenCache.Load(scope)
	if !ok {
		panic("test bug: cache entry for " + scope + " not present")
	}
	ct := v.(cachedToken)
	mut(&ct)
	be.tokenCache.Store(scope, ct)
}

// TestOCIRefreshSingleFlight exercises the in-flight coalescing: 8
// concurrent bearers against the same near-expiry token entry share
// ONE refresh POST, not 8. Without single-flight, a thundering-herd
// wake against a near-expiry token would fan out 8 refresh round trips.
func TestOCIRefreshSingleFlight(t *testing.T) {
	var refreshHits int32
	started := make(chan struct{}, 1) // signals when the first POST arrives
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt32(&refreshHits, 1)
			// Non-blocking signal: first writer wins, others see a
			// full channel and move on. We want every goroutine to
			// enter the cache-miss path BEFORE the first refresh
			// returns, otherwise the test would race.
			select {
			case started <- struct{}{}:
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"token":"refreshed","refresh_token":"rt-2","expires_in":3600}`)
			return
		}
		fmt.Fprintf(w, `{"token":"initial","refresh_token":"rt-1","expires_in":1}`)
	})
	var srv *httptest.Server
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			authChallenge(w, srv.URL, "repository:faas/apps:pull")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "x", "tags": []string{}})
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	be, err := NewOCIRegistryStorageBackend(
		WithRegistry(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatalf("NewOCIRegistryStorageBackend: %v", err)
	}
	ctx := context.Background()
	if _, err := be.fetchTags(ctx, "apps"); err != nil {
		t.Fatalf("seed fetchTags: %v", err)
	}
	manipulateCacheForTest(be, "repository:faas/apps:pull", func(ct *cachedToken) {
		ct.tok.ExpiresIn = 1 * time.Nanosecond
		ct.realmURL = srv.URL + "/token"
	})

	const concurrent = 8
	var wg sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = be.fetchTags(ctx, "apps")
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&refreshHits); got != 1 {
		t.Errorf("refresh hits = %d, want 1 (single-flight must coalesce)", got)
	}
	_ = started // referenced only to keep the channel alive in the closure
}
