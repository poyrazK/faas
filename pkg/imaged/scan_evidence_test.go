package imaged

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDigestScanArtifactHashesFileAndStagedDirectory(t *testing.T) {
	root := t.TempDir()
	contents := []byte("gregale scan evidence")
	file := filepath.Join(root, "rootfs.ext4")
	if err := os.WriteFile(file, contents, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	want := "sha256:"
	fileDigest, err := digestScanArtifact(file)
	if err != nil {
		t.Fatalf("digestScanArtifact(file): %v", err)
	}
	if !strings.HasPrefix(fileDigest, want) || len(fileDigest) != len(want)+64 {
		t.Fatalf("file digest = %q, want sha256 digest", fileDigest)
	}
	dirDigest, err := digestScanArtifact(root)
	if err != nil {
		t.Fatalf("digestScanArtifact(dir): %v", err)
	}
	if dirDigest != fileDigest {
		t.Fatalf("directory digest = %q, file digest = %q", dirDigest, fileDigest)
	}
}
