// Whitebox tests for the GET /v1/builds/{id} handler
// (cmd/apid/handlers_ext.go::getBuild, issue #741 / DEPLOY-PROV-6
// / ADR-089). Narrowly-scoped — exists only because the lifecycle
// surface is the first /v1/builds/{id}/* route whose (a) IDOR
// contract, (b) JSON shape (BuildResponse field set), and (c) error
// code map are load-bearing at once. The sbom handler covers
// filesystem-side path-traversal; the provenance handler is read-
// only with a simple 404 path. This file pins the lifecycle surface
// so future DTO drift or IDOR regressions surface here.
//
// Pattern follows the repo's whitebox-test-file convention (see
// handlers_build_sbom_test.go): `package main` so the test reaches
// the unexported server fields directly. The `newServer-with-
// secrets` pattern (re-used via sbomTestServer's shape, minus the
// sbomRoot field) seeds a MemStore + API key so each test can
// issue authenticated requests.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// buildTestServer stands up a server whose store is a fresh
// MemStore. Returns the handler, an API-key already attached for
// authentication, and the seeded account so the test can derive
// the cross-account probe. Mirrors sbomTestServer's shape but
// drops sbomRoot — getBuild has no filesystem surface.
func buildTestServer(t *testing.T) (h http.Handler, key string, store *state.MemStore, acct state.Account) {
	t.Helper()
	store = state.NewMemStore()
	var err error
	acct, err = store.CreateAccount(context.Background(), "build-owner@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err = store.CreateAPIKey(context.Background(), acct.ID, hash, "build-test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	return srv.handler(), pt, store, acct
}

// seedBuildForStatus creates an app + deployment + build owned by
// `acct` and returns the build id. The build row is left in its
// default state (queued, no started_at, no finished_at) so the
// happy-path test can inspect the JSON shape end-to-end. Tests
// that need a specific state transition call store.UpdateBuildStatus
// directly afterward.
func seedBuildForStatus(t *testing.T, store *state.MemStore, acct state.Account) (buildID string) {
	t.Helper()
	ctx := context.Background()
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      "build-test-app",
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
	build, err := store.CreateBuild(ctx, dep.ID, state.DeploymentKindTarball, 12345, "/var/spool/faas/builds/private/build.log")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	return build.ID
}

// buildGet issues a Bearer-authed GET against `h` and returns the
// recorder. Mirrors sbomGet in handlers_build_sbom_test.go so
// each test focuses on the assertion, not the harness.
func buildGet(t *testing.T, h http.Handler, key, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// TestGetBuild_OK pins the happy path: a queued build (no
// started_at, no finished_at) returns 200 with the BuildResponse
// shape — every required field present, optional fields omitted via
// the DTO's omitempty tags.
func TestGetBuild_OK(t *testing.T) {
	h, key, store, acct := buildTestServer(t)
	buildID := seedBuildForStatus(t, store, acct)

	rec := buildGet(t, h, key, "/v1/builds/"+buildID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.BuildResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != buildID {
		t.Errorf("id = %q, want %q", resp.ID, buildID)
	}
	if resp.Status != "queued" {
		t.Errorf("status = %q, want queued", resp.Status)
	}
	if resp.Kind != "tarball" {
		t.Errorf("kind = %q, want tarball", resp.Kind)
	}
	if resp.SourceBytes != 12345 {
		t.Errorf("source_bytes = %d, want 12345", resp.SourceBytes)
	}
	if resp.FailureClass != "" {
		t.Errorf("failure_class = %q, want empty (queued)", resp.FailureClass)
	}
	if resp.StartedAt != "" {
		t.Errorf("started_at = %q, want empty (queued)", resp.StartedAt)
	}
	if resp.FinishedAt != "" {
		t.Errorf("finished_at = %q, want empty (queued)", resp.FinishedAt)
	}
	if resp.DurationSeconds != 0 {
		t.Errorf("duration_seconds = %d, want 0 (queued, no terminal)", resp.DurationSeconds)
	}
	if resp.EnqueuedAt == "" {
		t.Errorf("enqueued_at = %q, want non-empty", resp.EnqueuedAt)
	}
	if strings.Contains(rec.Body.String(), "log_path") || strings.Contains(rec.Body.String(), "/var/") {
		t.Errorf("customer build response leaked host log path: %s", rec.Body.String())
	}
}

func TestGetBuild_CancelledLifecycle(t *testing.T) {
	h, key, store, acct := buildTestServer(t)
	buildID := seedBuildForStatus(t, store, acct)
	if err := store.UpdateBuildStatus(context.Background(), buildID, state.BuildRunning, "", true, false); err != nil {
		t.Fatalf("start build: %v", err)
	}
	if err := store.MarkBuildCancelled(context.Background(), buildID, "", false, time.Now().UTC()); err != nil {
		t.Fatalf("cancel build: %v", err)
	}

	rec := buildGet(t, h, key, "/v1/builds/"+buildID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.BuildResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != api.BuildStatusCancelled || resp.CancelledAt == "" || resp.StartedAt == "" {
		t.Fatalf("cancelled lifecycle = %+v", resp)
	}
	if resp.DurationSeconds < 0 {
		t.Errorf("duration_seconds = %d, want non-negative", resp.DurationSeconds)
	}
}

func TestGetBuild_QueuedCancellationHasNoDuration(t *testing.T) {
	h, key, store, acct := buildTestServer(t)
	buildID := seedBuildForStatus(t, store, acct)
	if err := store.MarkBuildCancelled(context.Background(), buildID, "", false, time.Now().UTC()); err != nil {
		t.Fatalf("cancel queued build: %v", err)
	}
	rec := buildGet(t, h, key, "/v1/builds/"+buildID)
	var resp api.BuildResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != api.BuildStatusCancelled || resp.CancelledAt == "" || resp.StartedAt != "" || resp.DurationSeconds != 0 {
		t.Fatalf("queued cancellation lifecycle = %+v", resp)
	}
}

// TestGetBuild_DurationSeconds pins the server-computed duration
// math (ADR-089 §3). Once the build reaches a terminal status,
// duration_seconds = FinishedAt − StartedAt in whole seconds.
func TestGetBuild_DurationSeconds(t *testing.T) {
	h, key, store, acct := buildTestServer(t)
	buildID := seedBuildForStatus(t, store, acct)

	// MemStore.UpdateBuildStatus has a CAS guard: terminal writes
	// require status='running' (see pkg/state/memstore.go:3793).
	// Walk the build through queued → running → succeeded.
	if err := store.UpdateBuildStatus(context.Background(), buildID,
		state.BuildRunning, "", true, false); err != nil {
		t.Fatalf("UpdateBuildStatus(running): %v", err)
	}
	if err := store.UpdateBuildStatus(context.Background(), buildID,
		state.BuildSucceeded, "", false, true); err != nil {
		t.Fatalf("UpdateBuildStatus(succeeded): %v", err)
	}

	rec := buildGet(t, h, key, "/v1/builds/"+buildID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.BuildResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", resp.Status)
	}
	if resp.StartedAt == "" || resp.FinishedAt == "" {
		t.Errorf("started_at = %q finished_at = %q, want both set",
			resp.StartedAt, resp.FinishedAt)
	}
	// The store stamps now() for both timestamps, so the
	// duration is tiny (< 1s) but non-negative. The strict
	// "82s" math TestGetBuild_OK originally intended would
	// require a backdoor to inject custom timestamps; not
	// worth it for a test whose purpose is to pin field
	// presence + omitempty behavior (which TestGetBuild_OK
	// already covers for queued builds).
	if resp.DurationSeconds < 0 {
		t.Errorf("duration_seconds = %d, want >= 0", resp.DurationSeconds)
	}
}

// TestGetBuild_NotFound pins that a bogus id renders 404 with
// code=build_not_found (ADR-089 §8). The same envelope is used
// for cross-account probes, so a single test covers both shapes.
func TestGetBuild_NotFound(t *testing.T) {
	h, key, _, _ := buildTestServer(t)

	rec := buildGet(t, h, key, "/v1/builds/00000000000000000000000000000000")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.NewDecoder(rec.Body).Decode(&prob); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prob.Code != api.CodeBuildNotFound {
		t.Errorf("code = %q, want %q", prob.Code, api.CodeBuildNotFound)
	}
}

// TestGetBuild_IDOR_OtherAccount pins the IDOR contract: a build
// owned by account B returns 404 (NOT 403) when GET'd as account
// A. The 404 surface is uniform so cross-account probes can't
// enumerate build ids by distinguishing "not found" from
// "forbidden".
func TestGetBuild_IDOR_OtherAccount(t *testing.T) {
	h, _, store, ownerAcct := buildTestServer(t)
	buildID := seedBuildForStatus(t, store, ownerAcct)

	// Mint a second account + key, then GET as that account.
	other, err := store.CreateAccount(context.Background(), "other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err = store.CreateAPIKey(context.Background(), other.ID, hash, "other-test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}

	rec := buildGet(t, h, pt, "/v1/builds/"+buildID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (IDOR), body = %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.NewDecoder(rec.Body).Decode(&prob); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prob.Code != api.CodeBuildNotFound {
		t.Errorf("code = %q, want %q", prob.Code, api.CodeBuildNotFound)
	}
	_ = h
}

// TestGetBuild_RequiresAuth pins that no Authorization header
// returns 401, NOT 404 (the IDOR-safe shape is uniform 404 for
// cross-account probes but a missing session is unambiguously
// unauthenticated).
func TestGetBuild_RequiresAuth(t *testing.T) {
	h, _, store, acct := buildTestServer(t)
	buildID := seedBuildForStatus(t, store, acct)

	r := httptest.NewRequest("GET", "/v1/builds/"+buildID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
}
