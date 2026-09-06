package builderd

import (
	"fmt"
	"os"
	"strings"
)

// statBuildArtifact validates the host-side export before builderd publishes
// it to the deployment row. CompleteBuild rejects zero-byte artifacts too,
// but doing that check here ensures the build is failed through the normal
// state transition instead of being left in running after an invalid export.
func statBuildArtifact(path string) (int64, error) {
	if strings.TrimSpace(path) == "" {
		return 0, fmt.Errorf("artifact path is empty")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return 0, fmt.Errorf("stat artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("artifact is not a regular file")
	}
	if info.Size() == 0 {
		return 0, fmt.Errorf("artifact is empty")
	}
	return info.Size(), nil
}
