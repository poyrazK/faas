package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParsePrepareChecksums(t *testing.T) {
	body := []byte(strings.Join([]string{
		strings.Repeat("a", 64) + "  release.tar.gz",
		strings.Repeat("b", 64) + "  release.cosign.bundle",
		strings.Repeat("c", 64) + " *release.sbom.json",
		strings.Repeat("d", 64) + "  production-manifest.yaml",
	}, "\n"))
	got, err := parsePrepareChecksums(body)
	if err != nil {
		t.Fatalf("parsePrepareChecksums: %v", err)
	}
	if got["release.sbom.json"] != strings.Repeat("c", 64) {
		t.Fatalf("sbom checksum = %q", got["release.sbom.json"])
	}
}

func TestPrepareReleaseAssetsReusesVerifiedCache(t *testing.T) {
	assets := map[string][]byte{
		"release.tar.gz":           []byte("tarball"),
		"release.cosign.bundle":    []byte("signature"),
		"release.sbom.json":        []byte("sbom"),
		"production-manifest.yaml": []byte("manifest"),
		"release-manifest.json":    []byte("release manifest"),
	}
	sums := make([]string, 0, len(prepareChecksumAssetNames))
	for _, name := range prepareChecksumAssetNames {
		sum := sha256.Sum256(assets[name])
		sums = append(sums, hex.EncodeToString(sum[:])+"  "+name)
	}
	assets["SHA256SUMS"] = []byte(strings.Join(sums, "\n") + "\n")

	var requests atomic.Int64
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			response := githubReleaseResponse{}
			for name := range assets {
				response.Assets = append(response.Assets, struct {
					Name               string `json:"name"`
					BrowserDownloadURL string `json:"browser_download_url"`
				}{Name: name, BrowserDownloadURL: server.URL + "/assets/" + name})
			}
			_ = json.NewEncoder(w).Encode(response)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		body, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	first, err := prepareReleaseAssets(t.Context(), prepareReleaseOptions{
		Repo: "poyrazK/faas", Tag: "v0.1.18-rc.15", CacheDir: cacheDir,
		APIBaseURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("first prepareReleaseAssets: %v", err)
	}
	if len(first.Assets) != len(prepareReleaseAssetNames)-1 {
		t.Fatalf("first asset report length = %d", len(first.Assets))
	}
	for _, asset := range first.Assets {
		if asset.CacheHit {
			t.Errorf("first run unexpectedly hit cache for %s", asset.Name)
		}
	}
	firstRequests := requests.Load()

	second, err := prepareReleaseAssets(t.Context(), prepareReleaseOptions{
		Repo: "poyrazK/faas", Tag: "v0.1.18-rc.15", CacheDir: cacheDir,
		APIBaseURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("second prepareReleaseAssets: %v", err)
	}
	if requests.Load() <= firstRequests {
		t.Fatal("second run did not refresh release metadata/checksum index")
	}
	for _, asset := range second.Assets {
		if !asset.CacheHit {
			t.Errorf("second run missed cache for %s", asset.Name)
		}
	}
}

func TestStagePrepareSecretsRejectsCAKey(t *testing.T) {
	secrets := t.TempDir()
	for name, body := range map[string]string{
		"compute-ssh-key": "private key",
		"compute-db.env":  "DATABASE_URL=postgres://faas@example/faas\nFAAS_VMMD_DBURL=postgres://faas@example/faas\n",
		"storage.env":     "FAAS_STORAGE_BACKEND=oci\nFAAS_STORAGE_LOCAL_PREFIXES=none\nFAAS_REQUIRE_SHARED_ARTIFACTS=1\nFAAS_STORAGE_CACHE_SERVE_STALE=0\nFAAS_OCI_REGISTRY=https://registry.example\n",
		"sign.key":        "signing key",
		"sign-pub.pem":    "public key",
	} {
		if err := os.WriteFile(filepath.Join(secrets, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(secrets, "pki", "ca"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "pki", "ca", "ca.crt"), []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "pki", "ca", "ca.key"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := stagePrepareSecrets(secrets, filepath.Join(t.TempDir(), "out"))
	if err == nil || !strings.Contains(err.Error(), "ca/ca.key") {
		t.Fatalf("stagePrepareSecrets error = %v, want CA key rejection", err)
	}
}

func TestCopyPrepareTreeClampsTrustBundlePermissions(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "bundle.pem"), []byte("certificate"), 0o666); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "out")
	if err := copyPrepareTree(source, destination); err != nil {
		t.Fatalf("copyPrepareTree: %v", err)
	}
	info, err := os.Stat(filepath.Join(destination, "bundle.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o444 {
		t.Fatalf("copied trust bundle mode = %o, want 444", got)
	}
}

func TestResolvePrepareCosignBinaryResolvesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cosign-real")
	if err := os.WriteFile(target, []byte("cosign"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "cosign")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := resolvePrepareCosignBinary(link)
	if err != nil {
		t.Fatalf("resolvePrepareCosignBinary: %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolved cosign = %q, want %q", got, want)
	}
}

func TestPrepareNodeRequiresOneProviderHandoff(t *testing.T) {
	if code := cmdDeployPrepareNode([]string{
		"--manifest-file", "manifest.yaml",
		"--release-tag", "v0.1.18-rc.15",
		"--secrets-dir", "secrets",
		"--output-dir", "output",
	}); code != 2 {
		t.Fatalf("cmdDeployPrepareNode exit code = %d, want 2", code)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("/tmp/a'b"); got != "'/tmp/a'\\''b'" {
		t.Fatalf("shellQuote = %q", got)
	}
}

func Example_cmdDeployPrepareNode() {
	fmt.Println("gregalectl deploy prepare-node --claim-file /secure/fsn-3.yaml --manifest-file /secure/production-manifest.yaml --release-tag v0.1.18-rc.15 --secrets-dir /secure/fleet-secrets --output-dir /secure/fleet/join-artifacts")
	// Output:
	// gregalectl deploy prepare-node --claim-file /secure/fsn-3.yaml --manifest-file /secure/production-manifest.yaml --release-tag v0.1.18-rc.15 --secrets-dir /secure/fleet-secrets --output-dir /secure/fleet/join-artifacts
}
