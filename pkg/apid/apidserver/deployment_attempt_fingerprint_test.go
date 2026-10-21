package apidserver_test

// adr: 20261021101500

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func fingerprintTarball(t *testing.T, body string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	content := []byte(body)
	if err := tw.WriteHeader(&tar.Header{Name: "index.js", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func attemptMismatch(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Code == api.CodeDeploymentAttemptMismatch
}

// A reused Deployment-Attempt-ID replays only the request it was accepted for.
func TestDeploymentAttemptFingerprintHTTP(t *testing.T) {
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "receipt", 123)
	srv := httptest.NewServer(*e.H())
	defer srv.Close()
	client := api.NewClient(srv.URL, *e.Key()).SetCompletionCache(nil)
	image := api.CreateDeploymentRequest{Image: "ghcr.io/onebox-faas/hello@sha256:" + strings.Repeat("a", 64)}

	imageAttempt := api.ContextWithDeploymentAttempt(t.Context(), strings.Repeat("1", 32))
	first, err := client.Deploy(api.ContextWithIdempotencyKey(imageAttempt, "image-1"), "receipt", image)
	if err != nil {
		t.Fatal(err)
	}
	// A retry with a new replay key but the same request recovers the deployment.
	retry, err := client.Deploy(api.ContextWithIdempotencyKey(imageAttempt, "image-2"), "receipt", image)
	if err != nil || retry.ID != first.ID {
		t.Fatalf("same request did not replay: %+v %v", retry, err)
	}
	other := api.CreateDeploymentRequest{Image: "ghcr.io/onebox-faas/hello@sha256:" + strings.Repeat("b", 64)}
	if _, err := client.Deploy(api.ContextWithIdempotencyKey(imageAttempt, "image-3"), "receipt", other); !attemptMismatch(err) {
		t.Fatalf("different image replayed the accepted attempt: %v", err)
	}

	sourceAttempt := api.ContextWithDeploymentAttempt(t.Context(), strings.Repeat("2", 32))
	source := fingerprintTarball(t, "console.log('one')\n")
	deploy := func(key string, tarball []byte) (api.DeploymentResponse, error) {
		return client.DeployMultipart(api.ContextWithIdempotencyKey(sourceAttempt, key), "receipt", bytes.NewReader(tarball), "src.tar.gz", "node22", "", false, api.DeployAnnotations{})
	}
	accepted, err := deploy("source-1", source)
	if err != nil {
		t.Fatal(err)
	}
	// Each multipart request uses a new boundary; the fingerprint ignores it.
	if again, err := deploy("source-2", source); err != nil || again.ID != accepted.ID {
		t.Fatalf("same source did not replay: %+v %v", again, err)
	}
	if _, err := deploy("source-3", fingerprintTarball(t, "console.log('two')\n")); !attemptMismatch(err) {
		t.Fatalf("different source replayed the accepted attempt: %v", err)
	}
	deps, err := (*e.Store()).ListDeploymentsForApp(t.Context(), *e.AppID(), 10, 0)
	if err != nil || len(deps) != 2 {
		t.Fatalf("mismatched attempts created deployments: %+v %v", deps, err)
	}
}
