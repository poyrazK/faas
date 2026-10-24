package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestResumableUploadClient_WireContract(t *testing.T) {
	var methods []string
	var patchBody []byte
	var patchOffset string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "Bearer fp_test" {
			t.Errorf("authorization = %q, want bearer token", got)
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("%s %s missing idempotency key", r.Method, r.URL.Path)
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads":
			var req resumableUploadStartRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode start request: %v", err)
			}
			if req.AppSlug != "demo" || req.TotalSize != 6 || req.Sha256Hex != nil || req.DeployOptions == nil || req.DeployOptions.SourceRoot != "apps/api" {
				t.Errorf("start request = %+v", req)
			}
			if req.DeployOptions.SourceURL != "github://acme/demo@0123456789abcdef0123456789abcdef01234567" || req.DeployOptions.CommitSHA != "0123456789abcdef0123456789abcdef01234567" {
				t.Errorf("provenance options = %+v", req.DeployOptions)
			}
			if req.DeployOptions.RollbackOn5xx == nil || !*req.DeployOptions.RollbackOn5xx {
				t.Errorf("rollback option = %+v, want explicit true", req.DeployOptions.RollbackOn5xx)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(resumableUploadStartResponse{
				UploadID: "upload-1", ChunkSize: 3, TotalSize: 6, ExpiresAt: "2030-01-01T00:00:00Z",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/uploads/upload-1":
			patchOffset = r.Header.Get("Upload-Offset")
			patchBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Upload-Offset", strconv.Itoa(6))
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/uploads/upload-1":
			_ = json.NewEncoder(w).Encode(UploadSessionResponse{UploadID: "upload-1", AppSlug: "demo", ChunkSize: 3, TotalSize: 6, ReceivedBytes: 3, Status: "open"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads/upload-1/commit":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(DeploymentResponse{ID: "dep-1", AppID: "demo", Status: "pending"})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/uploads/upload-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp_test").SetCompletionCache(nil)
	ctx := context.Background()
	rollback := true
	session, err := c.StartUpload(ctx, "demo", 6, "", UploadDeployOptions{
		SourceRoot:    "apps/api",
		SourceURL:     "github://acme/demo@0123456789abcdef0123456789abcdef01234567",
		CommitSHA:     "0123456789abcdef0123456789abcdef01234567",
		RollbackOn5xx: &rollback,
	})
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	if session.UploadID != "upload-1" || session.ChunkSize != 3 || session.TotalSize != 6 {
		t.Fatalf("session = %+v", session)
	}
	state, err := c.GetUploadSession(ctx, session.UploadID)
	if err != nil || state.ReceivedBytes != 3 || state.Status != "open" {
		t.Fatalf("GetUploadSession = %+v, %v", state, err)
	}

	next, err := c.AppendUpload(ctx, session.UploadID, 3, []byte("abc"))
	if err != nil {
		t.Fatalf("AppendUpload: %v", err)
	}
	if next != 6 || patchOffset != "3" || !bytes.Equal(patchBody, []byte("abc")) {
		t.Fatalf("append acknowledgment: next=%d offset=%q body=%q", next, patchOffset, patchBody)
	}

	dep, err := c.CommitUpload(ctx, session.UploadID)
	if err != nil {
		t.Fatalf("CommitUpload: %v", err)
	}
	if dep.ID != "dep-1" {
		t.Fatalf("deployment = %+v", dep)
	}
	if err := c.CancelUpload(ctx, session.UploadID); err != nil {
		t.Fatalf("CancelUpload: %v", err)
	}

	wantMethods := []string{
		"POST /v1/uploads",
		"GET /v1/uploads/upload-1",
		"PATCH /v1/uploads/upload-1",
		"POST /v1/uploads/upload-1/commit",
		"DELETE /v1/uploads/upload-1",
	}
	if len(methods) != len(wantMethods) {
		t.Fatalf("methods = %v, want %v", methods, wantMethods)
	}
	for i := range wantMethods {
		if methods[i] != wantMethods[i] {
			t.Errorf("methods[%d] = %q, want %q", i, methods[i], wantMethods[i])
		}
	}
}

// An apid from before deploy tokens could open upload sessions answers them
// 403. The CLI falls back to the legacy endpoint for a deploy token only; an
// account key's 403 is a real refusal.
func TestStartUpload_DeployToken403OnOlderServerFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		WriteProblem(w, NewProblem(http.StatusForbidden, CodeForbidden, "Deploy token scope is app-bound",
			"this token may only access routes under /v1/apps/{slug}"))
	}))
	defer srv.Close()
	deployToken := DeployTokenPrefix + strings.Repeat("a", apiKeyRandomBytes*2)
	if !ValidDeployTokenFormat(deployToken) {
		t.Fatal("test token does not have the deploy-token shape")
	}
	if _, err := NewClient(srv.URL, deployToken).StartUpload(context.Background(), "demo", 1, ""); !errors.Is(err, ErrResumableUploadUnsupported) {
		t.Fatalf("deploy token StartUpload error = %v, want ErrResumableUploadUnsupported", err)
	}
	if _, err := NewClient(srv.URL, "fp_live_account").StartUpload(context.Background(), "demo", 1, ""); err == nil || errors.Is(err, ErrResumableUploadUnsupported) {
		t.Fatalf("account key StartUpload error = %v, want the 403 reported", err)
	}
}

func TestStartUpload_404MeansUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	_, err := NewClient(srv.URL, "").StartUpload(context.Background(), "demo", 1, "")
	if !errors.Is(err, ErrResumableUploadUnsupported) {
		t.Fatalf("StartUpload error = %v, want ErrResumableUploadUnsupported", err)
	}
}
