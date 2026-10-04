package main

import (
	"os"
	"path/filepath"
	"testing"
)

// adr: 426 — source ingress preserves npm integrity while detecting credentials.
func TestScanExtractedMCPStarterPreservesSecretDefense(t *testing.T) {
	dir := t.TempDir()
	starter := filepath.Join("..", "gregale", "templates", "mcp-node")
	if err := os.CopyFS(dir, os.DirFS(starter)); err != nil {
		t.Fatal(err)
	}
	findings, err := scanExtractedTreeSecrets(dir)
	if err != nil || len(findings) != 0 {
		t.Fatalf("starter rejected: findings=%+v err=%v", findings, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credential.js"), []byte("const STRIPE_SECRET_KEY = \""+fakeStripeLiveKey+"\";"), 0600); err != nil {
		t.Fatal(err)
	}
	findings, err = scanExtractedTreeSecrets(dir)
	if err != nil || len(findings) != 1 || findings[0].Provider != "stripe_live" {
		t.Fatalf("credential accepted: findings=%+v err=%v", findings, err)
	}
}
