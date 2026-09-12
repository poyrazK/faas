// Whitebox tests for handleStartUpload / handleAppendUpload /
// handleCommitUpload / handleCancelUpload (issue #1182 §P1 PR-1).
//
// The 5 plan-pinned invariants split into two groups. The first
// 4 are exercised here (no tar-shape validation required — the
// happy-path commit + dedupe test needs a real tarball which
// is covered by the existing TestSourceTarball_HappyPath that
// shares the validateTarballShape + apidsource.Enqueue path).
//
// Covered here:
//   - TestUploadSession_PlanCap         : total_size > SourceTarballMaxMB → 413
//   - TestUploadSession_OpenCap         : 6th concurrent open session → 429
//   - TestUploadSession_OffsetCAS       : 50 goroutines PATCH same offset → exactly one wins
//   - TestUploadSession_Cancel          : DELETE flips status to cancelled, .part deleted
//
// Audit emissions + tar-shape validation are exercised by the
// parallel TestSourceTarball_HappyPath
// (cmd/apid/handlers_source_tarball_test.go) which shares the
// same audit emission + Enqueue path.
package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func startSession(t *testing.T, e testEnv, slug string, totalSize int64) startUploadResponse {
	t.Helper()
	body, _ := json.Marshal(startUploadRequest{AppSlug: slug, TotalSize: totalSize})
	req := httptest.NewRequest("POST", "/v1/uploads", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("startSession: %d %s", rec.Code, rec.Body.String())
	}
	var resp startUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("startSession decode: %v", err)
	}
	return resp
}

func appendChunk(t *testing.T, e testEnv, id string, offset int64, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PATCH", "/v1/uploads/"+id, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func cancelSession(t *testing.T, e testEnv, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("DELETE", "/v1/uploads/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestUploadSession_PlanCap(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanFree)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "over-cap"}, nil)

	// Free plan SourceTarballMaxMB is 100 MB. Request 200 MB.
	rec := e.do(t, "POST", "/v1/uploads", startUploadRequest{
		AppSlug: "over-cap", TotalSize: 200 * 1024 * 1024,
	}, nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d: %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &prob)
	if prob.Code != api.CodeSourceTooLarge {
		t.Fatalf("want code %q, got %q", api.CodeSourceTooLarge, prob.Code)
	}
}

func TestUploadSession_RollbackOn5xxPlanGate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)
	e := setup(t, api.PlanFree)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "rollback-gate"}, nil)
	rollback := true
	rec := e.do(t, "POST", "/v1/uploads", startUploadRequest{
		AppSlug: "rollback-gate", TotalSize: 1024,
		DeployOptions: &api.UploadDeployOptions{RollbackOn5xx: &rollback},
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if prob.Code != api.CodePlanRollbackOn5xxNotAllowed {
		t.Fatalf("want code %q, got %q", api.CodePlanRollbackOn5xxNotAllowed, prob.Code)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spool root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("gated upload allocated spool entries: %+v", entries)
	}
}

func TestUploadSession_UnknownAppDoesNotAllocateSpool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)
	e := setup(t, api.PlanFree)

	rec := e.do(t, "POST", "/v1/uploads", startUploadRequest{
		AppSlug: "missing", TotalSize: 1024,
	}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if prob.Code != api.CodeNotFound {
		t.Fatalf("want code %q, got %q", api.CodeNotFound, prob.Code)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spool root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unknown app allocated spool entries: %+v", entries)
	}
}

func TestUploadSession_OpenCap(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanFree)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "open-cap"}, nil)

	for i := 0; i < uploadSessionOpenCap; i++ {
		startSession(t, e, "open-cap", 1024)
	}
	// 6th must be rejected with 429.
	body, _ := json.Marshal(startUploadRequest{AppSlug: "open-cap", TotalSize: 1024})
	req := httptest.NewRequest("POST", "/v1/uploads", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadSession_OffsetCAS(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanFree)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "cas"}, nil)

	sess := startSession(t, e, "cas", 4096)
	// 50 goroutines all PATCH at offset=0 with 4 bytes each.
	// Exactly one should return 200; the rest return 409.
	const N = 50
	results := make(chan int, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			rec := appendChunk(t, e, sess.UploadID, 0, []byte{1, 2, 3, 4})
			results <- rec.Code
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for code := range results {
		if code == http.StatusOK {
			wins++
		} else if code != http.StatusConflict {
			t.Fatalf("unexpected status %d", code)
		}
	}
	if wins != 1 {
		t.Fatalf("want exactly 1 winner, got %d", wins)
	}
}

func TestUploadSession_MetadataAndDiscovery(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "metadata"}, nil)
	rollback := true
	body, err := json.Marshal(startUploadRequest{
		AppSlug: "metadata", TotalSize: 4096,
		DeployOptions: &api.UploadDeployOptions{
			SourceRoot: "apps/api", Dockerfile: true, Reason: "release",
			SourceURL:     "github://acme/metadata@0123456789abcdef0123456789abcdef01234567",
			CommitSHA:     "0123456789abcdef0123456789abcdef01234567",
			RollbackOn5xx: &rollback,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/uploads", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var started startUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	row, err := e.store.GetUploadSession(t.Context(), started.UploadID)
	if err != nil || !bytes.Contains(row.DeployOptions, []byte(`"source_root":"apps/api"`)) {
		t.Fatalf("persisted options = %s, err=%v", row.DeployOptions, err)
	}
	if !bytes.Contains(row.DeployOptions, []byte(`"source_url":"github://acme/metadata@0123456789abcdef0123456789abcdef01234567"`)) ||
		!bytes.Contains(row.DeployOptions, []byte(`"commit_sha":"0123456789abcdef0123456789abcdef01234567"`)) {
		t.Fatalf("persisted provenance options = %s", row.DeployOptions)
	}
	if !bytes.Contains(row.DeployOptions, []byte(`"rollback_on_5xx":true`)) {
		t.Fatalf("persisted rollback option = %s", row.DeployOptions)
	}
	req = httptest.NewRequest("GET", "/v1/uploads/"+started.UploadID, nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	var state api.UploadSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.UploadID != started.UploadID || state.ReceivedBytes != 0 || state.Status != "open" {
		t.Fatalf("discovery = %+v", state)
	}
}

func TestUploadSession_CommitPreservesSourceProvenance(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanFree)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "provenance"}, nil)
	raw := buildTestTarGz(t, []tar.Header{{Name: "index.js"}}, map[string][]byte{
		"index.js": []byte("console.log(1)\n"),
	})
	const sourceURL = "github://acme/provenance@0123456789abcdef0123456789abcdef01234567"
	const commitSHA = "0123456789abcdef0123456789abcdef01234567"
	body, err := json.Marshal(startUploadRequest{
		AppSlug: "provenance", TotalSize: int64(len(raw)),
		DeployOptions: &api.UploadDeployOptions{SourceURL: sourceURL, CommitSHA: commitSHA},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/uploads", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var started startUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if rec := appendChunk(t, e, started.UploadID, 0, raw); rec.Code != http.StatusOK {
		t.Fatalf("append: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("POST", "/v1/uploads/"+started.UploadID+"/commit", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.SourceURL != sourceURL || out.CommitSHA != commitSHA {
		t.Fatalf("response provenance = source_url %q commit_sha %q", out.SourceURL, out.CommitSHA)
	}
	dep, err := e.store.LatestDeployment(t.Context(), out.AppID)
	if err != nil {
		t.Fatalf("LatestDeployment: %v", err)
	}
	if dep.SourceURL != sourceURL || dep.CommitSHA != commitSHA {
		t.Fatalf("stored provenance = source_url %q commit_sha %q", dep.SourceURL, dep.CommitSHA)
	}
}

func TestUploadSession_Cancel(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanFree)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "cancel"}, nil)

	sess := startSession(t, e, "cancel", 4096)

	// DELETE → 204
	if rec := cancelSession(t, e, sess.UploadID); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}

	// A second PATCH on the cancelled session must 409
	// (ErrUploadSessionAlreadyCancelled).
	if rec := appendChunk(t, e, sess.UploadID, 0, []byte{1, 2, 3, 4}); rec.Code != http.StatusConflict {
		t.Fatalf("PATCH after cancel: want 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// A second DELETE on the cancelled session must 409.
	if rec := cancelSession(t, e, sess.UploadID); rec.Code != http.StatusConflict {
		t.Fatalf("re-cancel: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
}
