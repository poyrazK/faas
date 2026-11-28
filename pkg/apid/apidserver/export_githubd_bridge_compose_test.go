package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SpoolBuildSource runs the bridge's build-source spool for app with spool as
// the spool root, for package apidserver_test.
func SpoolBuildSource(ctx context.Context, spool, sourcePath string, app state.App) (string, int64, error) {
	return (&githubdBridge{spool: spool}).spoolBuildSource(ctx, sourcePath, app, api.PlanPro)
}
