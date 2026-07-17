package builderd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/state"
)

// log.go — append build log lines to the spool file the deployment row points
// at (state.Deployment.LogPath). The log is the source of truth for the
// streamed-build-log surface (UX spec §2.4); cmd/builderd may also serve an
// SSE endpoint that tails this file in M7 (out of scope here).

// appendLog opens (creates if missing) the build's log file and appends one
// line. It is best-effort: callers log + continue on error.
func appendLog(ctx context.Context, store state.Store, buildID, line string) error {
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
	if err := os.MkdirAll(filepath.Dir(dep.LogPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(dep.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("write log: %w", err)
	}
	return nil
}

