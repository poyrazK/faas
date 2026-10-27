package main_test

import (
	"os"
	"path/filepath"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestDryRunReadsManifestCompanions feeds deploy --dry-run the companions the
// deploy would send (Hunt 14 H14-53): the declared set, none without a
// manifest, and nothing when the manifest cannot be read.
func TestDryRunReadsManifestCompanions(t *testing.T) {
	declared := t.TempDir()
	manifest := "companions:\n  - name: web\n    image: registry.example.com/nginx@sha256:" +
		"aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899\n    port: 8080\n"
	if err := os.WriteFile(filepath.Join(declared, "gregale.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	companions, ok := main.PreviewCompanionsFromManifest(declared)
	if !ok || len(companions) != 1 || companions[0].Name != "web" || companions[0].Port != 8080 {
		t.Fatalf("declared companions = %+v (ok %v), want web on 8080", companions, ok)
	}
	if companions, ok := main.PreviewCompanionsFromManifest(t.TempDir()); !ok || companions != nil {
		t.Fatalf("no manifest = %+v (ok %v), want a known empty set", companions, ok)
	}
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "gregale.yaml"), []byte("companions: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := main.PreviewCompanionsFromManifest(broken); ok {
		t.Fatal("an unreadable manifest must leave companions out of the diff")
	}
	if _, ok := main.PreviewCompanionsFromManifest(""); ok {
		t.Fatal("no source directory must leave companions out of the diff")
	}
}
