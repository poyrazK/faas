package imaged

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/rootfs"
)

// H10-93: production imaged units run with ProtectSystem=strict and the apps
// root is neither writable nor present, so staging produced scans there failed
// every one with artifact_read and left new verified bases fail-closed. Scans
// stage under the temp dir, which the units point at writable storage.
func TestProducedScanStagesUnderTempDirNotAppsRoot(t *testing.T) {
	be := mustLocalStorage(t, t.TempDir())
	body := bytes.Repeat([]byte{7}, 4096)
	const key = "base/scan-scratch.ext4"
	if err := be.Put(t.Context(), key, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	identity, err := rootfs.ReadArtifactIdentity(t.Context(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	var scanned string
	h := &Handler{
		appsRoot: filepath.Join(t.TempDir(), "absent", "apps"),
		log:      silentLogger(),
		grypeRun: func(_ context.Context, path string) (*ScanResult, error) {
			scanned = path
			return producedScanResult(t, false), nil
		},
	}
	result, failure := h.readProducedArtifactScan(t.Context(), be, scanArtifactTarget{StorageKey: key, ArtifactIdentity: identity})
	if failure != "" || result == nil {
		t.Fatalf("produced scan failed with %q; want a result staged outside the apps root", failure)
	}
	if !strings.HasPrefix(scanned, tmp) {
		t.Fatalf("scan staged at %q, want under TMPDIR %q", scanned, tmp)
	}
}

// A produced scan that fails stores only its failure reason; imaged logs the
// reason and the cause, so a fail-closed base says why.
func TestProducedScanFailureIsLoggedWithItsCause(t *testing.T) {
	be := mustLocalStorage(t, t.TempDir())
	body := bytes.Repeat([]byte{7}, 4096)
	const key = "base/scan-failure.ext4"
	if err := be.Put(t.Context(), key, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	identity, err := rootfs.ReadArtifactIdentity(t.Context(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	var logs bytes.Buffer
	h := &Handler{
		log: slog.New(slog.NewTextHandler(&logs, nil)),
		grypeRun: func(context.Context, string) (*ScanResult, error) {
			return nil, errors.New("scan command grype failed: output exceeds 16777216 bytes")
		},
	}
	if _, failure := h.readProducedArtifactScan(t.Context(), be, scanArtifactTarget{StorageKey: key, ArtifactIdentity: identity}); failure != "scanner_unavailable" {
		t.Fatalf("failure = %q, want scanner_unavailable", failure)
	}
	for _, want := range []string{"produced artifact scan failed", key, "scanner_unavailable", "output exceeds 16777216 bytes"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %q lacks %q", logs.String(), want)
		}
	}
}
