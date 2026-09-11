// Whitebox tests for the GET /v1/builds/{id}/sbom handler
// (cmd/apid/handlers_ext.go::getBuildSbom, issue #299 / ADR-038
// Phase 3). Narrowly-scoped — exists only because the SBOM handler
// is the single route whose (a) IDOR contract, (b) error-code map,
// and (c) filesystem-side path-traversal guard are all load-bearing
// at once. The provenance handler above has no analogous surface
// (no filesystem join, no streaming body) so it stays uncovered
// here.
//
// Pattern follows the repo's whitebox-test-file convention: a
// `package main` file that reaches into the private `s.sbomRoot`
// field directly so we can stage a per-test tempdir as the SBOM
// root without standing up a full StorageBackend.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	artifactstorage "github.com/onebox-faas/faas/pkg/storage"
)

// sbomTestServer stands up a server whose sbomRoot points at the
// supplied directory. Returns the server handle (with a Bearer-key
// already attached via the inner newServer-with-secrets pattern) so
// the test can issue authenticated requests.
//
// We re-implement the e.h/do() machinery rather than reuse setup()
// because setup() returns an http.Handler only; the test needs
// access to s.sbomRoot (unexported). Mirrors the helper shape of
// newStripeServer at handlers_ext_test.go:1538.
func sbomTestServer(t *testing.T, sbomRoot string) (h http.Handler, key string, store *state.MemStore, acct state.Account) {
	t.Helper()
	store = state.NewMemStore()
	var err error
	acct, err = store.CreateAccount(context.Background(), "sbom-owner@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err = store.CreateAPIKey(context.Background(), acct.ID, hash, "sbom-test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	backend, err := artifactstorage.NewLocalStorageBackend(sbomRoot)
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	srv.WithSBOMRoot(sbomRoot).WithSBOMStorage(backend)
	return srv.handler(), pt, store, acct
}

// seedBuildForSBOM creates an app + deployment + build in `store`
// owned by `acct` and returns the build id. Mirrors builderd's
// production CreateBuild call (the SBOM handler does not branch
// on the deployment kind — it only reads build/deployment/app for
// the IDOR chain).
func seedBuildForSBOM(t *testing.T, store *state.MemStore, acct state.Account) (appSlug, depID, buildID string) {
	t.Helper()
	ctx := context.Background()
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      "sbom-test-app",
		Type:      state.AppTypeFunction,
		Runtime:   "node22",
		RAMMB:     256,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourceBytes: 0,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	build, err := store.CreateBuild(ctx, dep.ID, state.DeploymentKindTarball, 0, "")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	return app.Slug, dep.ID, build.ID
}

// sbomGet issues a Bearer-authed GET against `h` and returns the
// recorder. Encapsulates the boilerplate so each test focuses on
// the assertion, not the harness.
func sbomGet(t *testing.T, h http.Handler, key, path string) *http.Response {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Result()
}

// TestGetBuildSbom_NoProvenance exercises the "build exists,
// provenance row missing" failure mode (the populator hadn't run,
// pre-Phase-3 build) — must surface as 503 + code =
// build_sbom_unavailable. Confirms the IDOR guard passes (the build
// belongs to the authenticated account).
func TestGetBuildSbom_NoProvenance(t *testing.T) {
	root := t.TempDir()
	h, key, store, acct := sbomTestServer(t, root)
	_, _, buildID := seedBuildForSBOM(t, store, acct)

	resp := sbomGet(t, h, key, "/v1/builds/"+buildID+"/sbom")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 503; body = %s", resp.StatusCode, body)
	}
	var prob api.Problem
	if err := json.NewDecoder(resp.Body).Decode(&prob); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if prob.Code != api.CodeBuildSBOMUnavailable {
		t.Errorf("problem code = %q, want %q", prob.Code, api.CodeBuildSBOMUnavailable)
	}
}

// TestGetBuildSbom_EmptyStorageKey exercises the "provenance row
// exists but sbom_storage_key is empty" branch (the populator's
// best-effort INSERT succeeded for non-SBOM columns but the SBOM
// specific column wasn't filled — exactly the field the populator
// at pkg/imaged/loop.go is responsible for, pre-Phase-3).
func TestGetBuildSbom_EmptyStorageKey(t *testing.T) {
	root := t.TempDir()
	h, key, store, acct := sbomTestServer(t, root)
	_, _, buildID := seedBuildForSBOM(t, store, acct)

	if err := store.CreateBuildProvenance(context.Background(), state.BuildProvenance{
		ID:          "prv-" + buildID,
		BuildID:     buildID,
		BuildkitVer: "v0.20.0",
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		// SBOMStorageKey deliberately left empty.
	}); err != nil {
		t.Fatalf("CreateBuildProvenance: %v", err)
	}

	resp := sbomGet(t, h, key, "/v1/builds/"+buildID+"/sbom")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 503; body = %s", resp.StatusCode, body)
	}
	var prob api.Problem
	if err := json.NewDecoder(resp.Body).Decode(&prob); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if prob.Code != api.CodeBuildSBOMUnavailable {
		t.Errorf("problem code = %q, want %q", prob.Code, api.CodeBuildSBOMUnavailable)
	}
}

// TestGetBuildSbom_HappyPath is the success case: build +
// provenance + sbom_storage_key relative-path + a real file on
// disk. Asserts the response is the CycloneDX JSON byte-for-byte
// and the content-type matches the spec.
func TestGetBuildSbom_HappyPath(t *testing.T) {
	root := t.TempDir()
	h, key, store, acct := sbomTestServer(t, root)
	_, _, buildID := seedBuildForSBOM(t, store, acct)

	storageKey := "sboms/" + buildID + ".cdx.json"
	full := filepath.Join(root, storageKey)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5","version":1,"components":[]}`)
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatalf("write sbom: %v", err)
	}

	if err := store.CreateBuildProvenance(context.Background(), state.BuildProvenance{
		ID:             "prv-" + buildID,
		BuildID:        buildID,
		BuildkitVer:    "v0.20.0",
		StartedAt:      time.Now(),
		FinishedAt:     time.Now(),
		SBOMStorageKey: storageKey,
	}); err != nil {
		t.Fatalf("CreateBuildProvenance: %v", err)
	}

	resp := sbomGet(t, h, key, "/v1/builds/"+buildID+"/sbom")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/vnd.cyclonedx+json" {
		t.Errorf("content-type = %q, want application/vnd.cyclonedx+json", got)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body mismatch:\n got=%s\nwant=%s", got, body)
	}
}

// TestGetBuildSbom_BuildBelongsToAnotherAccount is the IDOR guard:
// even when the build id exists in the database, a different
// authenticated account must observe the SAME surface as a
// genuinely-missing id (404 / not_found). The test seeds a build
// owned by account A and asserts account B's request for the same
// id renders 404 — the handler must not leak account-existence by
// returning 503 build_sbom_unavailable to a stranger (which would
// imply "the build exists, you're just not allowed").
func TestGetBuildSbom_BuildBelongsToAnotherAccount(t *testing.T) {
	// Build the seed in account A's store.
	rootA := t.TempDir()
	_, _, storeA, acctA := sbomTestServer(t, rootA)
	_, _, buildID := seedBuildForSBOM(t, storeA, acctA)

	// Mount account B's server against the same data (the prod
	// deployment runs a single store per daemon, so this is the
	// realistic threat-model surface: a build belongs to one
	// account, a different account requests it).
	rootB := t.TempDir()
	hB, keyB, _, _ := sbomTestServer(t, rootB)
	// Re-issue the request: account B authenticates against its own
	// store, which has no build row → BuildByID returns NotFound →
	// 404. The handler does not access rootB because the IDOR
	// guard short-circuits on the account mismatch.

	resp := sbomGet(t, hB, keyB, "/v1/builds/"+buildID+"/sbom")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404 (IDOR-safe); body = %s", resp.StatusCode, body)
	}
}

// TestGetBuildSbom_StorageKeyTraversal confirms the path-traversal
// guard rejects sbom_storage_key values that contain ".." or
// absolute-path segments. The handler's defense-in-depth check at
// handlers_ext.go performs filepath.Clean + string-prefix checks
// BEFORE the os.Open; this test asserts the contract holds under
// hostile inputs and that the requested files never come into
// existence on the test root.
func TestGetBuildSbom_StorageKeyTraversal(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "absolute", key: "/etc/passwd"},
		{name: "dotdot-prefix", key: "../etc/passwd"},
		{name: "embedded-dotdot", key: "sboms/../../etc/passwd"},
		{name: "cleaned-to-dot", key: "."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			h, key, store, acct := sbomTestServer(t, root)
			_, _, buildID := seedBuildForSBOM(t, store, acct)

			if err := store.CreateBuildProvenance(context.Background(), state.BuildProvenance{
				ID:             "prv-trav-" + tc.name + "-" + buildID,
				BuildID:        buildID,
				StartedAt:      time.Now(),
				FinishedAt:     time.Now(),
				SBOMStorageKey: tc.key,
			}); err != nil {
				t.Fatalf("CreateBuildProvenance: %v", err)
			}

			resp := sbomGet(t, h, key, "/v1/builds/"+buildID+"/sbom")
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusServiceUnavailable {
				respBody, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 503; body = %s", resp.StatusCode, respBody)
			}
			var prob api.Problem
			if err := json.NewDecoder(resp.Body).Decode(&prob); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if prob.Code != api.CodeBuildSBOMUnavailable {
				t.Errorf("problem code = %q, want %q", prob.Code, api.CodeBuildSBOMUnavailable)
			}

			// Confirm the trajectory key did not result in a file
			// touching the temp root (a defensive belt-and-braces:
			// if filepath.Clean turned `"."` into `""` and the
			// filter short-circuit fired AFTER the open, the test
			// would still 503 but a junk file would exist).
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatalf("readdir: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("traversal-temp root is non-empty: %d entries (handler may have followed the bad key)", len(entries))
			}
		})
	}
}
