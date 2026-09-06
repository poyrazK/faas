package imaged

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the staged ext4 contract, including debugfs returning exit zero
// for a missing path. Go runtime bases intentionally have no compiler.
func TestValidateGoBaseWithoutCompiler(t *testing.T) {
	for _, command := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skip(command + " not installed")
		}
	}
	for _, missing := range []string{"", "/sbin/init", "/bin/sh", "/etc/passwd"} {
		t.Run("missing="+missing, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "root")
			for _, name := range []string{"/sbin/init", "/bin/sh", "/etc/passwd"} {
				if name == missing {
					continue
				}
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture\n"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			image := filepath.Join(dir, "base.ext4")
			cmd := exec.CommandContext(t.Context(), "mkfs.ext4", "-q", "-F", "-d", root, image, "16M")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("mkfs: %v: %s", err, output)
			}
			for _, variant := range []string{"go124", "go124-alpine"} {
				required := requiredBaseArtifactPaths("base/runner-" + variant + "-amd64.ext4")
				err := ValidateBaseArtifact(t.Context(), image, required)
				if missing == "" && err != nil {
					t.Fatalf("%s: %v", variant, err)
				}
				if missing != "" && (err == nil || !strings.Contains(err.Error(), missing)) {
					t.Fatalf("%s: error=%v, want missing %s", variant, err, missing)
				}
			}
		})
	}
}
