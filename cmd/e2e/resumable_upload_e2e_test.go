// resumable_upload_e2e_test.go — focused KVM-free coverage for the
// resumable source-upload commit boundary.
//
// These tests deliberately boot only APID. The commit path must be durable
// before builderd or any VM exists: a client can lose its process, retry a
// commit, or reconnect through another APID process without duplicating work
// or crossing an account boundary.
package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func resumableTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gz)
	for name, contents := range files {
		header := &tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(contents)),
			Typeflag: tar.TypeReg,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header %q: %v", name, err)
		}
		if _, err := tarWriter.Write([]byte(contents)); err != nil {
			t.Fatalf("write tar file %q: %v", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

// resumableRequest preserves the raw PATCH body and returns response headers;
// doReq is intentionally JSON-only and would base64-encode a []byte body.
func resumableRequest(t *testing.T, h *e2etest.Harness, key, method, path string, body []byte, extra map[string]string) (http.Header, []byte, int) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.APIDURL+path, reader)
	if err != nil {
		t.Fatalf("new resumable request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	for name, value := range extra {
		req.Header.Set(name, value)
	}
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return resp.Header, responseBody, resp.StatusCode
}

func decodeResumableProblem(t *testing.T, body []byte, status, wantStatus int, wantCode string) api.Problem {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("problem status=%d want %d body=%s", status, wantStatus, body)
	}
	var problem api.Problem
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatalf("decode problem: %v body=%s", err, body)
	}
	if problem.Code != wantCode {
		t.Fatalf("problem code=%q want %q body=%s", problem.Code, wantCode, body)
	}
	return problem
}

// TestE2E_ResumableUpload_RestartAndCommitReplay covers the client-visible
// protocol across an APID restart. It also pins the offset CAS and commit
// outcome dedupe, which are the two places a lost network response can turn
// into corruption or duplicate production work.
func TestE2E_ResumableUpload_RestartAndCommitReplay(t *testing.T) {
	f := newDeployLifecycleFixture(t, "resumable")
	if f == nil {
		return
	}

	source := resumableTarGz(t, map[string]string{
		"index.js": "console.log('resumable upload');\n",
	})
	digest := sha256.Sum256(source)
	digestHex := hex.EncodeToString(digest[:])
	commitSHA := strings.Repeat("a", 40)
	startRequest := api.UploadStartRequest{
		AppSlug:   f.app.Slug,
		TotalSize: int64(len(source)),
		Sha256Hex: &digestHex,
		DeployOptions: &api.UploadDeployOptions{
			SourceURL:  "https://github.com/example/resumable",
			CommitSHA:  commitSHA,
			Reason:     "resumable e2e",
			Tag:        "scheduled_maintenance",
			DeployedBy: "e2e",
		},
	}
	raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/uploads", startRequest)
	if status != http.StatusCreated {
		t.Fatalf("start upload: status=%d body=%s", status, raw)
	}
	var started api.UploadStartResponse
	if err := json.Unmarshal(raw, &started); err != nil {
		t.Fatalf("decode upload start: %v body=%s", err, raw)
	}
	if started.UploadID == "" || started.TotalSize != int64(len(source)) || started.ChunkSize <= 0 {
		t.Fatalf("upload start response = %+v", started)
	}

	split := len(source) / 2
	headers, body, status := resumableRequest(t, f.h, f.key, http.MethodPatch,
		"/v1/uploads/"+started.UploadID, source[:split], map[string]string{
			"Content-Type":  "application/offset+octet-stream",
			"Upload-Offset": strconv.Itoa(0),
		})
	if status != http.StatusOK || headers.Get("Upload-Offset") != strconv.Itoa(split) {
		t.Fatalf("first append: status=%d response_offset=%q body=%s", status, headers.Get("Upload-Offset"), body)
	}

	// Replaying the first chunk at offset zero must not overwrite or advance
	// the session. The error reports the server offset needed to resume.
	_, body, status = resumableRequest(t, f.h, f.key, http.MethodPatch,
		"/v1/uploads/"+started.UploadID, source[:split], map[string]string{
			"Content-Type":  "application/offset+octet-stream",
			"Upload-Offset": strconv.Itoa(0),
		})
	problem := decodeResumableProblem(t, body, status, http.StatusConflict, api.CodeUploadSessionOffsetConflict)
	if !strings.Contains(problem.Detail, strconv.Itoa(split)) {
		t.Fatalf("offset conflict detail=%q, want current offset %d", problem.Detail, split)
	}

	// The session is durable, so a new APID process can discover and continue
	// the same spool file. Harness.Stop is safe before test cleanup and does
	// not close the shared PostgreSQL pool.
	f.h.Stop()
	f.h = e2etest.Start(t, f.pool, e2etest.APID)

	_, body, status = resumableRequest(t, f.h, f.key, http.MethodGet,
		"/v1/uploads/"+started.UploadID, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("get upload after restart: status=%d body=%s", status, body)
	}
	var resumed api.UploadSessionResponse
	if err := json.Unmarshal(body, &resumed); err != nil {
		t.Fatalf("decode resumed upload: %v body=%s", err, body)
	}
	if resumed.UploadID != started.UploadID || resumed.Status != "open" || resumed.ReceivedBytes != int64(split) {
		t.Fatalf("resumed upload = %+v, want open at offset %d", resumed, split)
	}

	headers, body, status = resumableRequest(t, f.h, f.key, http.MethodPatch,
		"/v1/uploads/"+started.UploadID, source[split:], map[string]string{
			"Content-Type":  "application/offset+octet-stream",
			"Upload-Offset": strconv.Itoa(split),
		})
	if status != http.StatusOK || headers.Get("Upload-Offset") != strconv.Itoa(len(source)) {
		t.Fatalf("final append: status=%d response_offset=%q body=%s", status, headers.Get("Upload-Offset"), body)
	}

	_, body, status = resumableRequest(t, f.h, f.key, http.MethodPost,
		"/v1/uploads/"+started.UploadID+"/commit", nil, nil)
	if status != http.StatusCreated {
		t.Fatalf("commit upload: status=%d body=%s", status, body)
	}
	var committed api.DeploymentResponse
	if err := json.Unmarshal(body, &committed); err != nil {
		t.Fatalf("decode committed deployment: %v body=%s", err, body)
	}
	if committed.ID == "" || committed.BuildID == "" || committed.Kind != string(state.DeploymentKindTarball) || committed.Status != string(state.DeployPending) {
		t.Fatalf("committed deployment = %+v", committed)
	}
	if committed.SourceURL != "https://github.com/example/resumable" || committed.CommitSHA != commitSHA || committed.Tag != "scheduled_maintenance" {
		t.Fatalf("committed provenance = source_url:%q commit_sha:%q tag:%q", committed.SourceURL, committed.CommitSHA, committed.Tag)
	}

	dep, err := f.store.DeploymentByID(f.ctx, committed.ID)
	if err != nil {
		t.Fatalf("read committed deployment: %v", err)
	}
	if dep.SourceBytes != int64(len(source)) || dep.SourceSHA256 != digestHex {
		t.Fatalf("stored source identity = bytes:%d sha:%q, want bytes:%d sha:%q",
			dep.SourceBytes, dep.SourceSHA256, len(source), digestHex)
	}
	var deployments, builds int
	if err := f.pool.QueryRow(f.ctx, `select count(*) from deployments where app_id = $1`, f.app.ID).Scan(&deployments); err != nil {
		t.Fatalf("count committed deployments: %v", err)
	}
	if err := f.pool.QueryRow(f.ctx,
		`select count(*) from builds b join deployments d on d.id = b.deployment_id where d.app_id = $1`, f.app.ID).Scan(&builds); err != nil {
		t.Fatalf("count committed builds: %v", err)
	}
	if deployments != 1 || builds != 1 {
		t.Fatalf("commit rows = deployments:%d builds:%d, want 1/1", deployments, builds)
	}

	_, body, status = resumableRequest(t, f.h, f.key, http.MethodPost,
		"/v1/uploads/"+started.UploadID+"/commit", nil, nil)
	problem = decodeResumableProblem(t, body, status, http.StatusConflict, api.CodeUploadSessionAlreadyCommitted)
	if !strings.Contains(problem.Detail, committed.ID) {
		t.Fatalf("commit replay detail=%q, want deployment %s", problem.Detail, committed.ID)
	}
	_, body, status = resumableRequest(t, f.h, f.key, http.MethodGet,
		"/v1/uploads/"+started.UploadID, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("get committed upload: status=%d body=%s", status, body)
	}
	if err := json.Unmarshal(body, &resumed); err != nil {
		t.Fatalf("decode committed session: %v body=%s", err, body)
	}
	if resumed.Status != "committed" || resumed.DeploymentID == nil || *resumed.DeploymentID != committed.ID {
		t.Fatalf("committed session = %+v, want deployment %s", resumed, committed.ID)
	}
}

// TestE2E_ResumableUpload_OwnerIsolation prevents an upload ID from becoming
// a cross-account read, write, or commit capability. Every operation must
// look like a missing session to the wrong account.
func TestE2E_ResumableUpload_OwnerIsolation(t *testing.T) {
	f := newDeployLifecycleFixture(t, "resumable-idor")
	if f == nil {
		return
	}
	otherKey := f.h.SeedAccount(f.ctx, api.PlanPro, "resumable-idor-other")

	raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/uploads", api.UploadStartRequest{
		AppSlug: f.app.Slug, TotalSize: 4,
	})
	if status != http.StatusCreated {
		t.Fatalf("start owner upload: status=%d body=%s", status, raw)
	}
	var started api.UploadStartResponse
	if err := json.Unmarshal(raw, &started); err != nil {
		t.Fatalf("decode owner upload: %v body=%s", err, raw)
	}
	path := "/v1/uploads/" + started.UploadID

	_, body, status := resumableRequest(t, f.h, otherKey, http.MethodGet, path, nil, nil)
	decodeResumableProblem(t, body, status, http.StatusNotFound, api.CodeUploadSessionNotFound)
	_, body, status = resumableRequest(t, f.h, otherKey, http.MethodPatch, path, []byte("xxxx"), map[string]string{
		"Content-Type":  "application/offset+octet-stream",
		"Upload-Offset": "0",
	})
	decodeResumableProblem(t, body, status, http.StatusNotFound, api.CodeUploadSessionNotFound)
	_, body, status = resumableRequest(t, f.h, otherKey, http.MethodPost, path+"/commit", nil, nil)
	decodeResumableProblem(t, body, status, http.StatusNotFound, api.CodeUploadSessionNotFound)

	_, body, status = resumableRequest(t, f.h, f.key, http.MethodGet, path, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("owner session after IDOR attempts: status=%d body=%s", status, body)
	}
	var session api.UploadSessionResponse
	if err := json.Unmarshal(body, &session); err != nil {
		t.Fatalf("decode owner session after IDOR attempts: %v", err)
	}
	if session.ReceivedBytes != 0 || session.Status != "open" {
		t.Fatalf("owner session changed by IDOR attempt = %+v", session)
	}
	if _, _, status = resumableRequest(t, f.h, f.key, http.MethodDelete, path, nil, nil); status != http.StatusNoContent {
		t.Fatalf("cleanup owner upload: status=%d", status)
	}
}

// TestE2E_ResumableUpload_InvalidCommitIsRetryable verifies that validation
// failure does not create deployment/build rows and leaves the session open
// for an operator/client retry; explicit cancellation then removes the spool.
func TestE2E_ResumableUpload_InvalidCommitIsRetryable(t *testing.T) {
	f := newDeployLifecycleFixture(t, "resumable-invalid")
	if f == nil {
		return
	}

	invalid := []byte("not a gzip tarball")
	raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/uploads", api.UploadStartRequest{
		AppSlug: f.app.Slug, TotalSize: int64(len(invalid)),
	})
	if status != http.StatusCreated {
		t.Fatalf("start invalid upload: status=%d body=%s", status, raw)
	}
	var started api.UploadStartResponse
	if err := json.Unmarshal(raw, &started); err != nil {
		t.Fatalf("decode invalid upload: %v body=%s", err, raw)
	}
	path := "/v1/uploads/" + started.UploadID
	_, body, status := resumableRequest(t, f.h, f.key, http.MethodPatch, path, invalid, map[string]string{
		"Content-Type":  "application/offset+octet-stream",
		"Upload-Offset": "0",
	})
	if status != http.StatusOK {
		t.Fatalf("append invalid upload: status=%d body=%s", status, body)
	}

	_, body, status = resumableRequest(t, f.h, f.key, http.MethodPost, path+"/commit", nil, nil)
	decodeResumableProblem(t, body, status, http.StatusBadRequest, api.CodeSourceInvalid)

	var sessionStatus string
	var partPath string
	if err := f.pool.QueryRow(f.ctx,
		`select status, part_path from upload_sessions where id = $1`, started.UploadID).Scan(&sessionStatus, &partPath); err != nil {
		t.Fatalf("read invalid upload session: %v", err)
	}
	if sessionStatus != "open" {
		t.Fatalf("invalid commit changed session status to %q, want open", sessionStatus)
	}
	if _, err := os.Stat(partPath); err != nil {
		t.Fatalf("invalid commit removed retryable spool %q: %v", partPath, err)
	}
	var deployments, builds int
	if err := f.pool.QueryRow(f.ctx, `select count(*) from deployments where app_id = $1`, f.app.ID).Scan(&deployments); err != nil {
		t.Fatalf("count invalid deployments: %v", err)
	}
	if err := f.pool.QueryRow(f.ctx,
		`select count(*) from builds b join deployments d on d.id = b.deployment_id where d.app_id = $1`, f.app.ID).Scan(&builds); err != nil {
		t.Fatalf("count invalid builds: %v", err)
	}
	if deployments != 0 || builds != 0 {
		t.Fatalf("invalid commit created work = deployments:%d builds:%d, want 0/0", deployments, builds)
	}

	if _, _, status = resumableRequest(t, f.h, f.key, http.MethodDelete, path, nil, nil); status != http.StatusNoContent {
		t.Fatalf("cancel invalid upload: status=%d", status)
	}
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Fatalf("cancelled upload spool stat error=%v, want file removed", err)
	}
	var finalStatus string
	if err := f.pool.QueryRow(f.ctx,
		`select status from upload_sessions where id = $1`, started.UploadID).Scan(&finalStatus); err != nil {
		t.Fatalf("read cancelled upload: %v", err)
	}
	if finalStatus != "cancelled" {
		t.Fatalf("final upload status=%q, want cancelled", finalStatus)
	}
}
