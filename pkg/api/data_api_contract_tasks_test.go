package api_test

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIArtifactTransport func(*http.Request) (*http.Response, error)

func (f dataAPIArtifactTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDataAPIContractArtifactDownloadVerifiesDeploymentAndChecksum(t *testing.T) {
	content := strings.Repeat("a", (4<<20)+1)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	for _, scenario := range []string{"verified", "wrong deployment", "wrong checksum", "too large", "truncated"} {
		t.Run(scenario, func(t *testing.T) {
			client := api.NewClient("https://control.example", "owner-token")
			client.HTTPClient().Transport = dataAPIArtifactTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Path != "/v1/apps/orders/data-api/contract-tasks/task" ||
					req.URL.Query().Get("output") != "artifact" || req.Header.Get("Authorization") != "Bearer owner-token" {
					t.Fatalf("unexpected artifact request: %s %s", req.Method, req.URL)
				}
				response := &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(content)),
					ContentLength: int64(len(content)),
				}
				response.Header.Set("Content-Type", "application/octet-stream")
				response.Header.Set(api.DataAPIContractArtifactDeploymentHeader, "deployment")
				response.Header.Set(api.DataAPIContractArtifactSHA256Header, digest)
				switch scenario {
				case "wrong deployment":
					response.Header.Set(api.DataAPIContractArtifactDeploymentHeader, "earlier")
				case "wrong checksum":
					response.Header.Set(api.DataAPIContractArtifactSHA256Header, strings.Repeat("0", 64))
				case "too large":
					response.ContentLength = api.DataAPIContractArtifactBytes + 1
				case "truncated":
					response.Body = io.NopCloser(strings.NewReader(content[:16]))
				}
				return response, nil
			})
			got, err := client.DownloadDataAPIContractArtifact(t.Context(), "orders", "task", "deployment")
			if scenario == "verified" {
				if err != nil || string(got) != content {
					t.Fatalf("got %d bytes, err=%v", len(got), err)
				}
			} else if err == nil || got != nil {
				t.Fatalf("invalid download returned %d bytes, err=%v", len(got), err)
			}
		})
	}
}
