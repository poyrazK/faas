package builderd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/state"
)

// DefaultBuildLogMaxBytes keeps a noisy or malicious build from consuming
// the entire shared spool. The hard ceiling prevents a local configuration
// mistake from disabling the resource bound.
const DefaultBuildLogMaxBytes int64 = 8 << 20

const maxBuildLogMaxBytes int64 = 64 << 20

var buildLogAppendMu sync.Mutex

// log.go — append build log lines to the spool file the deployment row points
// at (state.Deployment.LogPath). The log is the source of truth for the
// streamed-build-log surface (UX spec §2.4); cmd/builderd may also serve an
// SSE endpoint that tails this file in M7 (out of scope here).

// appendLog preserves the legacy package-local helper for callers that do not
// have a configured spool root. Production builderd uses appendLogBounded.
//
//nolint:unused // retained for package-local compatibility with older tests
func appendLog(ctx context.Context, store state.Store, buildID, line string) error {
	return appendLogBounded(ctx, store, buildID, line, "", DefaultBuildLogMaxBytes)
}

// appendLogBounded opens (creates if missing) the build's log file, verifies
// that it is a regular file below the configured root, and appends at most the
// remaining bytes in the per-build budget. It is best-effort: callers log +
// continue on error.
func appendLogBounded(ctx context.Context, store state.Store, buildID, line, root string, maxBytes int64) error {
	build, err := store.BuildByID(ctx, buildID)
	if err != nil {
		return fmt.Errorf("load build: %w", err)
	}
	dep, err := store.DeploymentByID(ctx, build.DeploymentID)
	if err != nil {
		return fmt.Errorf("load deployment: %w", err)
	}
	if dep.LogPath == "" {
		return nil // image: deploys have no log; builderd never appends for those
	}
	if err := validateSourcePath(dep.LogPath, root); err != nil {
		return fmt.Errorf("validate log path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dep.LogPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := validateSourcePath(dep.LogPath, root); err != nil {
		return fmt.Errorf("validate log path after mkdir: %w", err)
	}
	maxBytes = effectiveBuildLogMaxBytes(maxBytes)
	buildLogAppendMu.Lock()
	defer buildLogAppendMu.Unlock()
	f, err := openNoFollow(dep.LogPath, unix.O_APPEND|unix.O_CREAT|unix.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat log: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("validate log: %w: %q is not a regular file", ErrSourceBoundary, dep.LogPath)
	}
	if info.Size() >= maxBytes {
		return nil
	}
	remaining := maxBytes - info.Size()
	data := []byte(line)
	if int64(len(data)) > remaining {
		data = data[:remaining]
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write log: %w", err)
	}
	return nil
}

func effectiveBuildLogMaxBytes(maxBytes int64) int64 {
	if maxBytes <= 0 {
		return DefaultBuildLogMaxBytes
	}
	if maxBytes > maxBuildLogMaxBytes {
		return maxBuildLogMaxBytes
	}
	return maxBytes
}
