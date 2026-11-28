package apidserver

import (
	"context"
	"io"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/state"
)

// SpoolBuildSource runs the bridge's build-source spool for app with spool as
// the spool root, for package apidserver_test.
func SpoolBuildSource(_ context.Context, spool, sourcePath string, app state.App) (string, int64, error) {
	b := &githubdBridge{spool: spool, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return b.spoolBuildSource(sourcePath, app)
}
