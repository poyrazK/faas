package registryproxy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/registryproxy"
)

// adr: 20261110210000 — slice 3j-3: a builder VM's BuildKit pulls a private
// base image from the mirror vmmd serves; every request reaches secretd by
// build id, at the real registry, with no credential.
func TestMirrorPullsThroughSecretdByBuild(t *testing.T) {
	sd := &fakeSecretd{answers: map[string]string{"https://ghcr.io/v2/team/base/manifests/v1": manifest}}
	mirror := &registryproxy.Mirror{Registry: "ghcr.io", Transport: (&registryproxy.Proxy{Client: sd}).ForBuild("build-1")}
	srv := httptest.NewServer(mirror)
	defer srv.Close()

	// The pull client speaks plain HTTP to the mirror, as BuildKit does with
	// http = true for 127.0.0.1.
	host := strings.TrimPrefix(srv.URL, "http://")
	client := oci.NewRegistryClient(oci.WithEndpoint("http", host))
	digest, err := client.PullDigestWithAuth(context.Background(), "ghcr.io/team/base:v1", nil)
	if err != nil {
		t.Fatalf("pull through the mirror = %v", err)
	}
	if digest == "" || len(sd.asked) == 0 {
		t.Fatalf("digest %q after %d asks", digest, len(sd.asked))
	}
	for _, in := range sd.asked {
		if in.GetBuildId() != "build-1" || in.GetAppId() != "" || in.GetJobId() != "" {
			t.Fatalf("asked %+v, want the build id alone", in)
		}
		if !strings.HasPrefix(in.GetUrl(), "https://ghcr.io/v2/team/base/") {
			t.Fatalf("asked %q, want the real registry", in.GetUrl())
		}
	}
}

func TestMirrorRefusesWhatIsNotAPull(t *testing.T) {
	sd := &fakeSecretd{answers: map[string]string{"https://ghcr.io/v2/team/base/blobs/sha256:a?x=1": "layer"}}
	srv := httptest.NewServer(&registryproxy.Mirror{Registry: "ghcr.io", Transport: (&registryproxy.Proxy{Client: sd}).ForBuild("build-1")})
	defer srv.Close()
	do := func(method, path string, header http.Header) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range header {
			req.Header[k] = v
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, string(body)
	}

	if resp, _ := do(http.MethodGet, "/v2/", nil); resp.StatusCode != 200 || resp.Header.Get("Docker-Distribution-API-Version") != "registry/2.0" {
		t.Fatalf("version check = %d %v", resp.StatusCode, resp.Header)
	}
	if len(sd.asked) != 0 {
		t.Fatal("the version check reached secretd")
	}
	for name, tc := range map[string]struct {
		method, path string
		want         int
	}{
		"push":             {http.MethodPut, "/v2/team/base/blobs/uploads/", http.StatusMethodNotAllowed},
		"post":             {http.MethodPost, "/v2/team/base/blobs/uploads/", http.StatusMethodNotAllowed},
		"outside /v2/":     {http.MethodGet, "/admin", http.StatusNotFound},
		"climbing out":     {http.MethodGet, "/v2/../token", http.StatusNotFound},
		"unknown manifest": {http.MethodGet, "/v2/team/other/manifests/v1", http.StatusNotFound},
	} {
		if resp, _ := do(tc.method, tc.path, nil); resp.StatusCode != tc.want {
			t.Errorf("%s = %d, want %d", name, resp.StatusCode, tc.want)
		}
	}

	// BuildKit's ns parameter is dropped, other query parameters kept, and
	// a guest's Authorization header never reaches secretd.
	resp, body := do(http.MethodGet, "/v2/team/base/blobs/sha256:a?ns=ghcr.io&x=1",
		http.Header{"Authorization": {"Bearer guest"}, "Range": {"bytes=0-"}})
	if resp.StatusCode != 200 || body != "layer" {
		t.Fatalf("blob = %d %q", resp.StatusCode, body)
	}
	last := sd.asked[len(sd.asked)-1]
	if last.GetUrl() != "https://ghcr.io/v2/team/base/blobs/sha256:a?x=1" {
		t.Fatalf("upstream url = %q", last.GetUrl())
	}
	for _, h := range last.GetHeaders() {
		if strings.EqualFold(h.GetName(), "Authorization") {
			t.Fatal("the guest's Authorization header reached secretd")
		}
	}

	// A refusal is a 502, never an anonymous pull by the mirror.
	sd.refuse = true
	if resp, _ := do(http.MethodGet, "/v2/team/base/blobs/sha256:a?x=1", nil); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("refused pull = %d, want 502", resp.StatusCode)
	}
}
