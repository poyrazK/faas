package imaged

import (
	"context"
	"strings"
	"testing"
)

func TestRequiredBaseArtifactPaths_RuntimeMatrix(t *testing.T) {
	cases := map[string]string{
		"base/runner-builder-amd64.ext4":   "faas-guest-init",
		"base/runner-node24-amd64.ext4":    "node",
		"base/runner-python313-amd64.ext4": "python3",
		"base/runner-go124-amd64.ext4":     "/usr/local/go/bin/go",
		"base/base-amd64.ext4":             "/bin/busybox",
	}
	for key, want := range cases {
		t.Run(key, func(t *testing.T) {
			paths := requiredBaseArtifactPaths(key)
			joined := strings.Join(paths, ",")
			if !strings.Contains(joined, want) {
				t.Fatalf("required paths = %v, want %q", paths, want)
			}
		})
	}
}

func TestValidateBaseArtifactRejectsUnsafeRequiredPath(t *testing.T) {
	err := ValidateBaseArtifact(context.Background(), "/tmp/does-not-matter.ext4", []string{"/bin/../etc/passwd"})
	if err == nil || !strings.Contains(err.Error(), "invalid required ext4 path") {
		t.Fatalf("ValidateBaseArtifact error = %v, want unsafe-path rejection", err)
	}
}
