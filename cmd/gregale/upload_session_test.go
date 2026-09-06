package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCanUseResumableUpload_PreservesRolloutFallback(t *testing.T) {
	cases := []struct {
		name           string
		runtime        string
		dockerfile     bool
		annotations    api.DeployAnnotations
		trafficPercent int
		sourceRoot     string
		want           bool
	}{
		{name: "plain app tarball", trafficPercent: -1, want: true},
		{name: "function runtime", runtime: "node22", trafficPercent: -1, want: true},
		{name: "dockerfile", dockerfile: true, trafficPercent: -1, want: true},
		{name: "traffic split", trafficPercent: 50},
		{name: "annotation", annotations: api.DeployAnnotations{Reason: "release"}, trafficPercent: -1, want: true},
		{name: "workspace source root", sourceRoot: "apps/api", trafficPercent: -1, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := canUseResumableUpload(shapeApp, tc.runtime, "", tc.dockerfile, tc.sourceRoot, tc.annotations, tc.trafficPercent, "", "")
			if got != tc.want {
				t.Fatalf("canUseResumableUpload() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeployResumableTarball_StreamsAndHashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.tar.gz")
	contents := []byte("abcdefg")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	var offsets []string
	var chunks [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"upload_id": "upload-1", "chunk_size": 3, "total_size": len(contents),
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/uploads/upload-1":
			off := r.Header.Get("Upload-Offset")
			offsets = append(offsets, off)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read chunk: %v", err)
			}
			chunks = append(chunks, body)
			w.Header().Set("Upload-Offset", strconv.Itoa(atoiForTest(t, off)+len(body)))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads/upload-1/commit":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-1", AppID: "demo", Status: "pending"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var progress []int64
	dep, digest, supported, err := DeployResumableTarball(
		api.NewClient(srv.URL, "fp_test").SetCompletionCache(nil),
		context.Background(), "demo", path,
		func(uploaded, _ int64) { progress = append(progress, uploaded) },
	)
	if err != nil {
		t.Fatalf("DeployResumableTarball: %v", err)
	}
	if !supported || dep.ID != "dep-1" {
		t.Fatalf("supported=%v deployment=%+v", supported, dep)
	}
	wantDigestBytes := sha256.Sum256(contents)
	if digest != hex.EncodeToString(wantDigestBytes[:]) {
		t.Fatalf("digest = %q, want %q", digest, hex.EncodeToString(wantDigestBytes[:]))
	}
	if got, want := fmt.Sprint(offsets), "[0 3 6]"; got != want {
		t.Fatalf("offsets = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(chunks), "[[97 98 99] [100 101 102] [103]]"; got != want {
		t.Fatalf("chunks = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(progress), "[3 6 7]"; got != want {
		t.Fatalf("progress = %s, want %s", got, want)
	}
}

func TestUploadSessionErrorDetails(t *testing.T) {
	offsetErr := &api.APIError{Problem: api.Problem{
		Code:   uploadSessionOffsetConflictCode,
		Detail: "expected offset 0; server is at 7",
	}}
	if got, ok := currentUploadOffset(offsetErr); !ok || got != 7 {
		t.Fatalf("currentUploadOffset() = %d, %v; want 7, true", got, ok)
	}
	committedErr := &api.APIError{Problem: api.Problem{
		Code:   uploadSessionAlreadyCommittedCode,
		Detail: "upload session already committed as deployment dep-123",
	}}
	if got := committedDeploymentID(committedErr); got != "dep-123" {
		t.Fatalf("committedDeploymentID() = %q, want dep-123", got)
	}
}

func atoiForTest(t *testing.T, raw string) int {
	t.Helper()
	n, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("parse offset %q: %v", raw, err)
	}
	return n
}
