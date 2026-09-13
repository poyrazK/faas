package fcvm

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestHandoffBuildExportOwnershipUsesParentContract(t *testing.T) {
	root := t.TempDir()
	exportDir := filepath.Join(root, "build-id")
	artifact := filepath.Join(exportDir, "build", "out", "image.tar")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("oci"), 0o400); err != nil {
		t.Fatal(err)
	}
	for dir := filepath.Dir(artifact); dir != root; dir = filepath.Dir(dir) {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
	}
	if err := handoffBuildExportOwnership(exportDir); err != nil {
		t.Fatal(err)
	}
	parentInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	artifactInfo, err := os.Stat(artifact)
	if err != nil {
		t.Fatal(err)
	}
	parentStat := parentInfo.Sys().(*syscall.Stat_t)
	artifactStat := artifactInfo.Sys().(*syscall.Stat_t)
	if artifactStat.Uid != parentStat.Uid || artifactStat.Gid != parentStat.Gid {
		t.Fatalf("artifact owner = %d:%d, want parent %d:%d", artifactStat.Uid, artifactStat.Gid, parentStat.Uid, parentStat.Gid)
	}
	if artifactInfo.Mode().Perm()&0o640 != 0o640 {
		t.Fatalf("artifact mode = %o, want owner/group readable", artifactInfo.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(artifact))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm()&0o750 != 0o750 {
		t.Fatalf("export child mode = %o, want owner cleanup/group traversal", dirInfo.Mode().Perm())
	}
	if err := os.RemoveAll(exportDir); err != nil {
		t.Fatalf("handed-off export is not removable: %v", err)
	}
}
