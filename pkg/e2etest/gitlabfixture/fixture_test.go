package gitlabfixture

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gitlabsource"
)

func fixtureDefinition() api.EnvironmentDefinition {
	return api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{"api": {App: "shop-api", Variables: map[string]string{"MODE": "reviewed"}}}}
}

func fixtureClient(t *testing.T, f *Fixture) gitlabsource.Client {
	t.Helper()
	ca, err := os.ReadFile(f.caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid fixture CA")
	}
	proxy, err := url.Parse(f.proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	t.Cleanup(transport.CloseIdleConnections)
	return gitlabsource.Client{HTTP: &http.Client{Transport: transport, Timeout: 5 * time.Second}}
}

func TestFixturePinsAuthorityIdentityAndArchiveThroughVerifiedTLS(t *testing.T) {
	f := New(t)
	sha := strings.Repeat("a", 40)
	f.AddFiles(t, sha, fixtureDefinition(), map[string][]byte{"jobs/report/commit.txt": []byte(sha)})
	client := fixtureClient(t, f)
	for _, tc := range []struct {
		name, token, repository string
		id                      int64
		unavailable, pass       bool
	}{
		{"control", f.token, Repository, RepositoryID, false, true},
		{"wrong OAuth account", "other", Repository, RepositoryID, false, false},
		{"project transfer", f.token, Repository, 42, false, false},
		{"different path", f.token, "other/shop", RepositoryID, false, false},
		{"outage", f.token, Repository, RepositoryID, true, false},
		{"recovery", f.token, Repository, RepositoryID, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.SetUnavailable(tc.unavailable)
			_, err := client.VerifyRepository(t.Context(), tc.token, tc.repository, tc.id)
			if (err == nil) != tc.pass {
				t.Fatalf("repository authority: %v", err)
			}
		})
	}
	resolved, err := client.ResolveCommit(t.Context(), f.token, RepositoryID, "refs/heads/main")
	if err != nil || resolved != sha {
		t.Fatalf("branch identity: %q %v", resolved, err)
	}
	archive, err := client.Archive(t.Context(), f.token, RepositoryID, sha, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	raw, err := io.ReadAll(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == "shop-"+sha+"/jobs/report/commit.txt" {
			data, err := io.ReadAll(tr)
			if err != nil || string(data) != sha {
				t.Fatal("GitLab build context differs from reviewed commit")
			}
			found = true
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("GitLab provider omitted the source build context")
	}
	desired, err := environmentgitops.ReadGitDefinition(bytes.NewReader(raw), "environments/production.yaml", 1<<20)
	if err != nil || desired.Definition.Workloads["api"].Variables["MODE"] != "reviewed" {
		t.Fatalf("approved archive: %+v %v", desired, err)
	}
	if _, err := client.Archive(t.Context(), f.token, RepositoryID, strings.Repeat("b", 40), 1<<20); err == nil {
		t.Fatal("unknown commit archive accepted")
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodConnect, f.proxy.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "other.example:443"
	response, err := f.proxy.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("proxy accepted a different origin")
	}
}

// Fresh processes are essential: Go caches environment proxy/root-CA state.
// This proves the same fixed-origin default client used by the real apid can
// consume the fixture through environment-scoped trust, without APIURL or a
// transport override and without disabling certificate verification.
func TestFixtureWorksWithProductionDefaultClientInFreshProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SSL_CERT_FILE trust is a Linux daemon contract; macOS uses the platform trust store")
	}
	f := New(t)
	f.Add(t, strings.Repeat("a", 40), fixtureDefinition())
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestFixtureDefaultClientChild$", "-test.v")
	cmd.Env = append(os.Environ(), f.Env()...)
	cmd.Env = append(cmd.Env, "GREGALE_GITLAB_FIXTURE_CHILD=1", "GREGALE_GITLAB_FIXTURE_TOKEN="+f.token)
	output, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("--- PASS: TestFixtureDefaultClientChild")) {
		t.Fatalf("default TLS client child failed: %v\n%s", err, output)
	}
}

func TestFixtureDefaultClientChild(t *testing.T) {
	if os.Getenv("GREGALE_GITLAB_FIXTURE_CHILD") != "1" {
		t.Skip("default-client subprocess only")
	}
	client, token := gitlabsource.Client{}, os.Getenv("GREGALE_GITLAB_FIXTURE_TOKEN")
	if _, err := client.VerifyRepository(t.Context(), token, Repository, RepositoryID); err != nil {
		t.Fatal(err)
	}
	sha, err := client.ResolveCommit(t.Context(), token, RepositoryID, "refs/heads/main")
	if err != nil || sha != strings.Repeat("a", 40) {
		t.Fatalf("default-client commit: %q %v", sha, err)
	}
	archive, err := client.Archive(t.Context(), token, RepositoryID, sha, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if _, err := io.Copy(io.Discard, archive); err != nil {
		t.Fatal(err)
	}
}
