package imaged

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaseMinimalProvidesMiseNPMCommands(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	dockerfile, err := os.ReadFile(filepath.Join(repoRoot, "images", "base-minimal.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	validator, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "ci", "validate-runtime-image.sh"))
	if err != nil {
		t.Fatal(err)
	}

	for _, command := range []string{"dirname", "basename"} {
		copyLine := "COPY --from=busybox /bin/busybox /usr/bin/" + command
		if !strings.Contains(string(dockerfile), copyLine) {
			t.Errorf("base-minimal is missing mise npm dependency %q", copyLine)
		}
		entrypoint := "--entrypoint /usr/bin/" + command
		if !strings.Contains(string(validator), entrypoint) {
			t.Errorf("runtime image validation does not execute /usr/bin/%s", command)
		}
	}
}
