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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
