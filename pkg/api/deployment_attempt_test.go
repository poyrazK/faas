package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Reuse a live connection, then lose a submitted request's reply. Go's
// transport normally retries keyed POSTs here, hiding the first attempt.
func TestDeploymentSubmissionDoesNotHideLostReply(t *testing.T) {
	for _, mode := range []string{"image", "commit", "source-ref", "multipart", "source-tarball"} {
		t.Run(mode, func(t *testing.T) {
			attempts := 0
			var keys []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(AppResponse{ID: "app-1", Slug: "demo"})
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				attempts++
				keys = append(keys, r.Header.Get("Idempotency-Key"))
				if attempts == 1 {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				}
				WriteProblem(w, NewProblem(422, "rejected", "Rejected", "Later attempt rejected"))
			}))
			defer server.Close()
			client := NewClient(server.URL, "fp_fixture").SetCompletionCache(nil)
			if _, err := client.GetApp(t.Context(), "demo"); err != nil {
				t.Fatal(err)
			}
			ctx := ContextWithIdempotencyKey(t.Context(), "same-intent")
			if mode != "commit" {
				ctx = ContextWithDeploymentAttempt(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			}
			submit := func() error {
				var err error
				switch mode {
				case "image":
					_, err = client.Deploy(ctx, "demo", CreateDeploymentRequest{Image: "example.test/image@sha256:abc"})
				case "commit":
					_, err = client.CommitUpload(ctx, "upload-1")
				case "source-ref":
					_, err = client.DeployFromSourceRef(ctx, "demo", SourceRefDeployRequest{Repo: "o/r", Ref: "main"})
				case "multipart":
					_, err = client.DeployMultipart(ctx, "demo", strings.NewReader("source"), "src.tar.gz", "node22", "", false, DeployAnnotations{})
				case "source-tarball":
					_, err = client.DeployFromSourceTarball(ctx, "demo", strings.NewReader("source"), "src.tar.gz", SourceTarballDeployRequest{})
				}
				return err
			}
			err := submit()
			var remote *APIError
			if err == nil || errors.As(err, &remote) || attempts != 1 {
				t.Fatalf("lost response concealed: attempts=%d err=%v", attempts, err)
			}
			err = submit()
			if !errors.As(err, &remote) || remote.Problem.Status != 422 || attempts != 2 || len(keys) != 2 || keys[0] != "same-intent" || keys[1] != keys[0] {
				t.Fatalf("explicit retry: attempts=%d keys=%v err=%v", attempts, keys, err)
			}
		})
	}
}
