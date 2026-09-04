package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
			if req.AppSlug != "demo" || req.TotalSize != 6 || req.Sha256Hex != nil {
				t.Errorf("start request = %+v", req)
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
	session, err := c.StartUpload(ctx, "demo", 6, "")
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	if session.UploadID != "upload-1" || session.ChunkSize != 3 || session.TotalSize != 6 {
		t.Fatalf("session = %+v", session)
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

func TestStartUpload_404MeansUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	_, err := NewClient(srv.URL, "").StartUpload(context.Background(), "demo", 1, "")
	if err != ErrResumableUploadUnsupported {
		t.Fatalf("StartUpload error = %v, want ErrResumableUploadUnsupported", err)
	}
}
