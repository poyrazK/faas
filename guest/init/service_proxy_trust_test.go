package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPrepareServiceProxyTrustBundleForSidecar(t *testing.T) {
	mainRoot := t.TempDir()
	sidecarRoot := t.TempDir()
	for _, root := range []string{mainRoot, sidecarRoot} {
		if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(mainRoot, "etc/faas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainRoot, serviceProxyCAPath), []byte("PRIVATE-CA\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sidecarRoot, "etc/ssl/certs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecarRoot, systemCABundlePaths[0]), []byte("PUBLIC-ROOTS\n"), 0o444); err != nil {
		t.Fatal(err)
	}

	paths, err := prepareServiceProxyTrust(mainRoot, sidecarRoot)
	if err != nil {
		t.Fatalf("prepareServiceProxyTrust: %v", err)
	}
	if paths.bundle != serviceProxyCABundleEnvPath || paths.nodeCA != serviceProxyCANodeEnvPath {
		t.Fatalf("trust paths = %+v", paths)
	}
	bundle, err := os.ReadFile(filepath.Join(sidecarRoot, strings.TrimPrefix(paths.bundle, "/")))
	if err != nil {
		t.Fatal(err)
	}
	if string(bundle) != "PUBLIC-ROOTS\nPRIVATE-CA\n" {
		t.Fatalf("combined bundle = %q", bundle)
	}
	nodeCA, err := os.ReadFile(filepath.Join(sidecarRoot, strings.TrimPrefix(paths.nodeCA, "/")))
	if err != nil {
		t.Fatal(err)
	}
	if string(nodeCA) != "PRIVATE-CA\n" {
		t.Fatalf("Node extra CA = %q, want only the Gregale CA", nodeCA)
	}
}

func TestPrepareServiceProxyTrustAbsentCAIsNoop(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths, err := prepareServiceProxyTrust(root, root)
	if err != nil {
		t.Fatalf("prepareServiceProxyTrust without CA: %v", err)
	}
	if paths != (serviceProxyTrustPaths{}) {
		t.Fatalf("trust paths = %+v, want empty", paths)
	}
}

func TestPrepareServiceProxyTrustReplacesTmpSymlinkWithoutFollowingIt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tmp", "unused"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc/faas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, serviceProxyCAPath), []byte("PRIVATE-CA\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.pem")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, strings.TrimPrefix(serviceProxyCABundleEnvPath, "/"))); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareServiceProxyTrust(root, root); err != nil {
		t.Fatalf("prepareServiceProxyTrust: %v", err)
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "unchanged" {
		t.Fatalf("outside target changed to %q", got)
	}
	info, err := os.Lstat(filepath.Join(root, strings.TrimPrefix(serviceProxyCABundleEnvPath, "/")))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("generated bundle path is still a symlink")
	}
}

func TestPrepareServiceProxyTrustIsSafeForConcurrentRestarts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc/faas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, serviceProxyCAPath), []byte("PRIVATE-CA\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < cap(errCh); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := prepareServiceProxyTrust(root, root)
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent prepareServiceProxyTrust: %v", err)
		}
	}
	for _, path := range []string{serviceProxyCABundleEnvPath, serviceProxyCANodeEnvPath} {
		if _, err := os.Stat(filepath.Join(root, strings.TrimPrefix(path, "/"))); err != nil {
			t.Errorf("generated file %s: %v", path, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("tmp contains %d entries after concurrent writes, want only the two bundles", len(entries))
	}
}

func TestStampServiceProxyTrustEnvOverridesManagedKeys(t *testing.T) {
	paths := serviceProxyTrustPaths{bundle: serviceProxyCABundleEnvPath, nodeCA: serviceProxyCANodeEnvPath, systemRoots: true}
	got := StampServiceProxyTrustEnv([]string{
		"KEEP=1",
		"SSL_CERT_FILE=/customer/roots.pem",
		"NODE_EXTRA_CA_CERTS=/customer/node.pem",
		"NODE_EXTRA_CA_CERTS=/another/node.pem",
		"REQUESTS_CA_BUNDLE=/customer/requests.pem",
	}, paths)
	values := make(map[string][]string)
	for _, entry := range got {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = append(values[key], value)
		}
	}
	for _, key := range []string{serviceProxyCABundleEnv, serviceProxyOpenSSLCAEnv, serviceProxyPythonRequestsCAEnv, serviceProxyCurlCAEnv} {
		if len(values[key]) != 1 || values[key][0] != paths.bundle {
			t.Errorf("%s = %v, want exactly %q", key, values[key], paths.bundle)
		}
	}
	if len(values[serviceProxyNodeCAEnv]) != 1 || values[serviceProxyNodeCAEnv][0] != paths.nodeCA {
		t.Errorf("%s = %v, want exactly %q", serviceProxyNodeCAEnv, values[serviceProxyNodeCAEnv], paths.nodeCA)
	}
	if len(values["KEEP"]) != 1 || values["KEEP"][0] != "1" {
		t.Errorf("unrelated env lost: %v", got)
	}
}

func TestStampServiceProxyTrustEnvKeepsDefaultsWithoutSystemRoots(t *testing.T) {
	paths := serviceProxyTrustPaths{bundle: serviceProxyCABundleEnvPath, nodeCA: serviceProxyCANodeEnvPath}
	got := StampServiceProxyTrustEnv([]string{
		"SSL_CERT_FILE=/image/custom-roots.pem",
		"REQUESTS_CA_BUNDLE=/image/requests-roots.pem",
		"CURL_CA_BUNDLE=/image/curl-roots.pem",
	}, paths)
	want := map[string]string{
		"SSL_CERT_FILE":         "/image/custom-roots.pem",
		"REQUESTS_CA_BUNDLE":    "/image/requests-roots.pem",
		"CURL_CA_BUNDLE":        "/image/curl-roots.pem",
		serviceProxyCABundleEnv: paths.bundle,
		serviceProxyNodeCAEnv:   paths.nodeCA,
	}
	gotValues := make(map[string]string)
	for _, entry := range got {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			gotValues[key] = value
		}
	}
	for key, value := range want {
		if gotValues[key] != value {
			t.Errorf("%s = %q, want %q", key, gotValues[key], value)
		}
	}
}
