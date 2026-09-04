package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

func TestNormalizeArtifactDigest(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, raw := range []string{digest, "sha256:" + digest, " SHA256:" + digest + " "} {
		got, err := normalizeArtifactDigest(raw)
		if err != nil {
			t.Fatalf("normalizeArtifactDigest(%q): %v", raw, err)
		}
		if got != digest {
			t.Errorf("normalizeArtifactDigest(%q) = %q, want %q", raw, got, digest)
		}
	}
	for _, raw := range []string{"", "sha256:bad", strings.Repeat("g", 64), strings.Repeat("0", 63)} {
		if _, err := normalizeArtifactDigest(raw); err == nil {
			t.Errorf("normalizeArtifactDigest(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestLoadStorageEnvDoesNotExecuteAndRestores(t *testing.T) {
	t.Setenv("FAAS_STORAGE_BACKEND", "ambient")
	path := filepath.Join(t.TempDir(), "storage.env")
	body := "# comments and blank lines are accepted\n" +
		"export FAAS_STORAGE_BACKEND=oci\n" +
		"FAAS_STORAGE_LOCAL_PREFIXES=none\n" +
		"FAAS_REQUIRE_SHARED_ARTIFACTS=1\n" +
		"FAAS_OCI_PASSWORD='literal;$(touch /tmp/should-not-exist)'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cleanup, err := loadStorageEnv(path)
	if err != nil {
		t.Fatalf("loadStorageEnv: %v", err)
	}
	if got := os.Getenv("FAAS_STORAGE_BACKEND"); got != "oci" {
		t.Errorf("FAAS_STORAGE_BACKEND = %q, want oci", got)
	}
	if got := os.Getenv("FAAS_OCI_PASSWORD"); got != "literal;$(touch /tmp/should-not-exist)" {
		t.Errorf("FAAS_OCI_PASSWORD was not parsed literally: %q", got)
	}
	cleanup()
	if got := os.Getenv("FAAS_STORAGE_BACKEND"); got != "ambient" {
		t.Errorf("cleanup restored backend = %q, want ambient", got)
	}
	if _, ok := os.LookupEnv("FAAS_OCI_PASSWORD"); ok {
		t.Error("cleanup left an environment password behind")
	}
}

func TestLoadStorageEnvRejectsUnknownAssignments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.env")
	if err := os.WriteFile(path, []byte("FAAS_NOT_ALLOWED=1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := loadStorageEnv(path); err == nil {
		t.Fatal("loadStorageEnv unexpectedly accepted an unknown assignment")
	}
}

func TestValidateArtifactStorageContract(t *testing.T) {
	t.Setenv("FAAS_STORAGE_BACKEND", "oci")
	t.Setenv("FAAS_REQUIRE_SHARED_ARTIFACTS", "1")
	t.Setenv("FAAS_STORAGE_LOCAL_PREFIXES", "none")
	if err := validateArtifactStorageContract(); err != nil {
		t.Fatalf("valid shared artifact contract: %v", err)
	}

	for name, value := range map[string]string{
		"FAAS_STORAGE_BACKEND":          "local",
		"FAAS_REQUIRE_SHARED_ARTIFACTS": "0",
		"FAAS_STORAGE_LOCAL_PREFIXES":   "kernel/",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			if err := validateArtifactStorageContract(); err == nil {
				t.Fatalf("contract unexpectedly accepted %s=%q", name, value)
			}
		})
	}
}

func TestLoadStorageEnvRejectsDuplicateAssignments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.env")
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_STORAGE_BACKEND=oci\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := loadStorageEnv(path); err == nil {
		t.Fatal("loadStorageEnv unexpectedly accepted a duplicate assignment")
	}
}

func TestPublishArtifactIsImmutableAndIdempotent(t *testing.T) {
	body := []byte("release kernel")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	path := filepath.Join(t.TempDir(), "vmlinux")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	be := newMemoryArtifactBackend()

	first, err := publishArtifact(context.Background(), be, "kernel/1.7.0", digest, path)
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if first.AlreadyPresent {
		t.Error("first publish reported already_present")
	}
	if first.Bytes != int64(len(body)) || first.SHA256 != digest {
		t.Errorf("first report = %+v", first)
	}

	second, err := publishArtifact(context.Background(), be, "kernel/1.7.0", digest, path)
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if !second.AlreadyPresent {
		t.Error("second publish did not report already_present")
	}

	if _, err := publishArtifact(context.Background(), be, "kernel/1.7.0", strings.Repeat("0", 64), path); err == nil {
		t.Fatal("publish with a mismatched release digest unexpectedly succeeded")
	}
}

func TestVerifyArtifactMissing(t *testing.T) {
	be := newMemoryArtifactBackend()
	_, err := verifyArtifact(context.Background(), be, "kernel/1.7.0", strings.Repeat("0", 64))
	if !storage.IsNotFound(err) {
		t.Fatalf("verify missing error = %v, want storage.ErrNotFound", err)
	}
}

type memoryArtifactBackend struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryArtifactBackend() *memoryArtifactBackend {
	return &memoryArtifactBackend{data: make(map[string][]byte)}
}

func (m *memoryArtifactBackend) Put(_ context.Context, key string, r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.data[key] = append([]byte(nil), body...)
	m.mu.Unlock()
	return nil
}

func (m *memoryArtifactBackend) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	body, ok := m.data[key]
	m.mu.Unlock()
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (m *memoryArtifactBackend) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.data, key)
	m.mu.Unlock()
	return nil
}

var _ storage.StorageBackend = (*memoryArtifactBackend)(nil)
