package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDevSourceFingerprintTracksDeployableFiles(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := devSourceFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}

	ignoredDir := filepath.Join(dir, "node_modules")
	if err := os.Mkdir(ignoredDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignoredDir, "noise.js"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	ignored, err := devSourceFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ignored != first {
		t.Fatal("default-excluded file changed developer source fingerprint")
	}

	if err := os.WriteFile(source, []byte("package main\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(time.Second)
	if err := os.Chtimes(source, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	changed, err := devSourceFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("source edit did not change developer source fingerprint")
	}
}
