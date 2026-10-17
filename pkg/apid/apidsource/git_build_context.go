package apidsource

import (
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/environmentgitops"
)

// CanonicalGitBuildContext publishes a bounded isolated context to the internal
// source spool. The caller supplies an already verified provider archive reader.
// Failed writes are removed; the accepted output is group-readable by builderd.
func CanonicalGitBuildContext(input io.Reader, spoolDirectory, buildContext string, maxBytes int64) (string, int64, error) {
	output, err := os.CreateTemp(spoolDirectory, "git-context-*.tar.gz")
	if err != nil {
		return "", 0, err
	}
	done := false
	defer func() {
		_ = output.Close()
		if !done {
			_ = os.Remove(output.Name())
		}
	}()
	if err := environmentgitops.CanonicalBuildContext(input, output, buildContext, maxBytes); err != nil {
		return "", 0, err
	}
	if err := output.Chmod(0o640); err != nil {
		return "", 0, err
	}
	if err := output.Sync(); err != nil {
		return "", 0, err
	}
	info, err := output.Stat()
	if err != nil {
		return "", 0, err
	}
	if err := output.Close(); err != nil {
		return "", 0, err
	}
	done = true
	return output.Name(), info.Size(), nil
}
