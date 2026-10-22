package faasschema_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A tracked symlink that leaves the repository resolves only on the machine
// that committed it. #4880 committed bin -> /tmp/..., which left every clean
// checkout with a dangling bin and broke make build.
func TestNoTrackedSymlinkLeavesRepository(t *testing.T) {
	out, err := exec.Command("git", "ls-files", "-s").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		meta, path, ok := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) < 2 || fields[0] != "120000" {
			continue
		}
		target, err := exec.Command("git", "cat-file", "-p", fields[1]).Output()
		if err != nil {
			t.Fatal(err)
		}
		dest := string(target)
		if filepath.IsAbs(dest) || !filepath.IsLocal(filepath.Join(filepath.Dir(path), dest)) {
			t.Errorf("tracked symlink %s -> %s points outside the repository", path, dest)
		}
	}
}
