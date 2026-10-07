package imaged

// adr: 435. Scan artifact opens reject customer symlink substitution.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenStagedScanArtifactRejectsLinksAndDirectories(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "artifact.ext4")
	if err := os.WriteFile(artifact, []byte("private scan bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.ext4")
	if err := os.Symlink(artifact, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, root} {
		f, err := openStagedScanArtifact(path)
		if f != nil || !errors.Is(err, errScanArtifactMismatch) {
			t.Fatalf("unsafe artifact was opened: %s, %v", path, err)
		}
	}
	f, err := openStagedScanArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
