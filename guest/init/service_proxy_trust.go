package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	serviceProxyCAPath              = "etc/faas/service-proxy-ca.crt"
	serviceProxyCABundleEnvPath     = "/tmp/.gregale-service-proxy-ca-bundle.pem"
	serviceProxyCANodeEnvPath       = "/tmp/.gregale-service-proxy-ca.pem"
	serviceProxyCABundleEnv         = "GREGALE_SERVICE_CA_BUNDLE"
	serviceProxyNodeCAEnv           = "NODE_EXTRA_CA_CERTS"
	serviceProxyOpenSSLCAEnv        = "SSL_CERT_FILE"
	serviceProxyPythonRequestsCAEnv = "REQUESTS_CA_BUNDLE"
	serviceProxyCurlCAEnv           = "CURL_CA_BUNDLE"
)

var systemCABundlePaths = []string{
	"etc/ssl/certs/ca-certificates.crt",
	"etc/ssl/cert.pem",
	"etc/pki/tls/certs/ca-bundle.crt",
	"etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
}

type serviceProxyTrustPaths struct {
	bundle      string
	nodeCA      string
	systemRoots bool
}

// prepareServiceProxyTrust makes a workload-scoped bundle on its writable
// /tmp. The guest CA is staged in the main root; each full-rootfs sidecar gets
// its own copy in its private /tmp so read-only image roots stay untouched.
// Missing CA material is the normal opt-out path and leaves workloads alone.
func prepareServiceProxyTrust(mainRoot, workloadRoot string) (serviceProxyTrustPaths, error) {
	if mainRoot == "" {
		mainRoot = "/"
	}
	if workloadRoot == "" {
		workloadRoot = "/"
	}
	serviceCA, err := readFileUnderRoot(mainRoot, serviceProxyCAPath)
	if errors.Is(err, fs.ErrNotExist) {
		return serviceProxyTrustPaths{}, nil
	}
	if err != nil {
		return serviceProxyTrustPaths{}, err
	}
	if len(serviceCA) == 0 {
		return serviceProxyTrustPaths{}, errors.New("service proxy CA bundle is empty")
	}

	var bundle []byte
	hasSystemRoots := false
	seen := make(map[string]struct{}, len(systemCABundlePaths))
	for _, path := range systemCABundlePaths {
		certs, readErr := readFileUnderRoot(workloadRoot, path)
		if errors.Is(readErr, fs.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return serviceProxyTrustPaths{}, readErr
		}
		if len(certs) == 0 {
			continue
		}
		key := string(certs)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		bundle = appendPEM(bundle, certs)
		hasSystemRoots = true
	}
	bundle = appendPEM(bundle, serviceCA)

	if err := writeFileUnderRoot(workloadRoot, strings.TrimPrefix(serviceProxyCABundleEnvPath, "/"), bundle); err != nil {
		return serviceProxyTrustPaths{}, err
	}
	if err := writeFileUnderRoot(workloadRoot, strings.TrimPrefix(serviceProxyCANodeEnvPath, "/"), serviceCA); err != nil {
		return serviceProxyTrustPaths{}, err
	}
	return serviceProxyTrustPaths{bundle: serviceProxyCABundleEnvPath, nodeCA: serviceProxyCANodeEnvPath, systemRoots: hasSystemRoots}, nil
}

func appendPEM(dst, certs []byte) []byte {
	dst = append(dst, certs...)
	if len(dst) > 0 && dst[len(dst)-1] != '\n' {
		dst = append(dst, '\n')
	}
	return dst
}

func readFileUnderRoot(rootPath, relativePath string) ([]byte, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.FromSlash(relativePath))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

func writeFileUnderRoot(rootPath, relativePath string, contents []byte) error {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	name := filepath.FromSlash(relativePath)
	// Publish through a same-directory temporary file: workload restarts can
	// overlap, and the application may have planted a symlink at the final path.
	// A random exclusive temp name avoids clobbering another writer; Rename
	// atomically replaces the final entry itself without following symlinks.
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("generate service trust temp name: %w", err)
	}
	tempName := name + "." + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return err
	}
	n, writeErr := file.Write(contents)
	if writeErr == nil && n != len(contents) {
		writeErr = io.ErrShortWrite
	}
	closeErr := file.Close()
	if writeErr != nil {
		_ = root.Remove(tempName)
		return writeErr
	}
	if closeErr != nil {
		_ = root.Remove(tempName)
		return closeErr
	}
	if err := root.Rename(tempName, name); err != nil {
		_ = root.Remove(tempName)
		return err
	}
	return nil
}

// StampServiceProxyTrustEnv gives common TLS clients the workload-scoped
// bundle while preserving their normal public roots. It is called only when
// a service CA is configured, and removes any customer value for these
// platform-managed variables before appending the effective paths.
func StampServiceProxyTrustEnv(env []string, paths serviceProxyTrustPaths) []string {
	if paths.bundle == "" || paths.nodeCA == "" {
		return env
	}
	for _, value := range []struct{ key, path string }{
		{serviceProxyCABundleEnv, paths.bundle},
		{serviceProxyNodeCAEnv, paths.nodeCA},
	} {
		env = replaceEnvValue(env, value.key, value.path)
	}
	// Do not replace a runtime's default trust path with a private-only file
	// for images that ship no discoverable system CA bundle. Node's additive
	// extra-CA mechanism remains safe without a system bundle.
	if paths.systemRoots {
		for _, value := range []struct{ key, path string }{
			{serviceProxyOpenSSLCAEnv, paths.bundle},
			{serviceProxyPythonRequestsCAEnv, paths.bundle},
			{serviceProxyCurlCAEnv, paths.bundle},
		} {
			env = replaceEnvValue(env, value.key, value.path)
		}
	}
	return env
}

func replaceEnvValue(env []string, key, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, ok := cut(entry)
		if !ok || name != key {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}
