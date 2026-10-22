//go:build metal && managed_service_native

package e2e_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A full-rootfs deployment still boots guest-init from drive0. Patch the
// accepted host base through the production copy-and-verify path so the
// private image contains the exact guest-init binary used by this test.
// Never mutate the host's shared /srv/fc tree during acceptance.
func stageNativeAcceptanceBase(t *testing.T, storageRoot string) {
	t.Helper()
	source := strings.TrimSpace(os.Getenv("FAAS_TEST_MINIMAL_BASE_PATH"))
	if source == "" {
		t.Fatal("FAAS_TEST_MINIMAL_BASE_PATH must name a validated generic base ext4")
	}
	guestInitPath := strings.TrimSpace(os.Getenv("FAAS_GUEST_INIT"))
	guestInit, err := os.Open(guestInitPath)
	if err != nil {
		t.Fatalf("open native acceptance guest-init: %v", err)
	}
	guestHash := sha256.New()
	_, hashErr := io.Copy(guestHash, guestInit)
	closeErr := guestInit.Close()
	if hashErr != nil || closeErr != nil {
		t.Fatalf("hash native acceptance guest-init: read=%v close=%v", hashErr, closeErr)
	}
	guestDigest := hex.EncodeToString(guestHash.Sum(nil))
	key := sched.BaseKeyForArch("", "amd64")
	backend, err := storage.NewLocalStorageBackend(storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rootfs.NewBuilder(wire.ExecRunner{}).PatchBaseGuestInit(t.Context(), rootfs.BasePatchInput{
		SourceImage: source, GuestInitPath: guestInitPath, GuestInitSHA256: guestDigest,
		Storage: backend, StorageKey: key,
	}); err != nil {
		t.Fatalf("patch private native acceptance base: %v", err)
	}
	dest := filepath.Join(storageRoot, filepath.FromSlash(key))
	sourceDigest, err := os.ReadFile(source + ".digest")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(sourceDigest)), "\n")
	updated := false
	for i, line := range lines {
		if strings.HasPrefix(line, "guest-init-sha256=") {
			lines[i] = "guest-init-sha256=" + guestDigest
			updated = true
		}
	}
	if !updated {
		t.Fatal("native acceptance base digest sidecar has no guest-init provenance")
	}
	if err := os.WriteFile(dest+".digest", []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	image, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	contentHash := sha256.New()
	size, hashErr := io.Copy(contentHash, image)
	closeErr = image.Close()
	if hashErr != nil || closeErr != nil {
		t.Fatalf("hash private native acceptance base: read=%v close=%v", hashErr, closeErr)
	}
	content, err := json.Marshal(struct {
		Version int    `json:"version"`
		SHA256  string `json:"sha256"`
		Size    int64  `json:"size"`
	}{Version: 1, SHA256: "sha256:" + hex.EncodeToString(contentHash.Sum(nil)), Size: size})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".content", content, 0o644); err != nil {
		t.Fatal(err)
	}
	// This locally patched base is a lifecycle fixture, not Grype evidence.
	// Model the admission sidecar explicitly so the VM can exercise the
	// reviewed workload's native boot and qualification path. The image
	// identity must match vmmd's configured test-only base generation.
	baseRef := os.Getenv("FAAS_TEST_DEPLOY_BASE_REF")
	if baseRef == "" {
		t.Fatal("native acceptance base has no configured generation")
	}
	scan, err := json.Marshal(struct {
		Image                string         `json:"image"`
		Findings             map[string]int `json:"findings"`
		FixAvailableFindings map[string]int `json:"fix_available_findings"`
	}{
		Image:    baseRef,
		Findings: map[string]int{"CRITICAL": 0}, FixAvailableFindings: map[string]int{"CRITICAL": 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(t.Context(), wire.ScanKeyForBaseKey(key), bytes.NewReader(scan)); err != nil {
		t.Fatalf("publish private base admission fixture: %v", err)
	}
}
