package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeploymentAttemptHeaderOnSubmissionTransports(t *testing.T) {
	for _, mode := range []string{"image", "multipart", "source-ref", "source-tarball", "upload"} {
		t.Run(mode, func(t *testing.T) {
			id := strings.Repeat("a", 32)
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get(DeploymentAttemptHeader) != id || r.Header.Get("Idempotency-Key") != "retry-intent" {
					t.Errorf("headers=%v", r.Header)
				}
				_, _ = io.Copy(io.Discard, r.Body)
				_ = json.NewEncoder(w).Encode(DeploymentResponse{ID: "dep-1"})
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "fixture").SetCompletionCache(nil)
			ctx := ContextWithDeploymentAttempt(ContextWithIdempotencyKey(t.Context(), "retry-intent"), id)
			var err error
			switch mode {
			case "upload":
				_, err = c.CommitUpload(ctx, "upload-1")
			case "image":
				_, err = c.Deploy(ctx, "demo", CreateDeploymentRequest{Image: "example/image:tag"})
			case "multipart":
				_, err = c.DeployMultipart(ctx, "demo", strings.NewReader("source"), "source.tar.gz", "node22", "", false, DeployAnnotations{})
			case "source-ref":
				_, err = c.DeployFromSourceRef(ctx, "demo", SourceRefDeployRequest{Repo: "o/r", Ref: "main"})
			case "source-tarball":
				_, err = c.DeployFromSourceTarball(ctx, "demo", strings.NewReader("source"), "source.tar.gz", SourceTarballDeployRequest{})
			}
			if err != nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestDeploymentReceiptLookupDoesNotSendAttemptHeader(t *testing.T) {
	id := strings.Repeat("b", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/deployment-submissions/"+id || r.Header.Get(DeploymentAttemptHeader) != "" {
			t.Errorf("request=%v", r)
		}
		_ = json.NewEncoder(w).Encode(DeploymentSubmissionReceipt{AttemptID: id, AppID: "app-1", DeploymentID: "dep-1", AcceptedAt: time.Now()})
	}))
	defer srv.Close()
	r, err := NewClient(srv.URL, "fixture").GetDeploymentSubmission(ContextWithDeploymentAttempt(context.Background(), id), "demo", id)
	if err != nil || r.AttemptID != id || r.DeploymentID != "dep-1" {
		t.Fatalf("receipt=%+v err=%v", r, err)
	}
}
