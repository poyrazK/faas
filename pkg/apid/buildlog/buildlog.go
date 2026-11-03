// Package buildlog tells a build log reader when retention deleted the log
// (ADR-20261102182856). meterd deletes a build's deployment_logs rows
// meter.BuildLogRetentionDays after its last line unless the deployment is
// live or among its app's api.BuildLogRetainedDeployments newest; an empty
// log for such a deployment is an expired log, not a missing one.
package buildlog

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// NewestLister is the store read Expired needs.
type NewestLister interface {
	ListDeploymentsForApp(ctx context.Context, appID string, limit, offset int) ([]state.Deployment, error)
}

// Expired reports whether retention may have deleted d's build log, for a
// caller that found no rows: d was built (image deployments have no build
// log), is not live, was created before the retention window (its last line
// cannot be newer than its creation) and is not among its app's newest
// deployments.
func Expired(ctx context.Context, store NewestLister, d state.Deployment, now time.Time) (bool, error) {
	if d.Kind == state.DeploymentKindImage || d.Status == state.DeployLive {
		return false, nil
	}
	if !d.CreatedAt.Before(now.AddDate(0, 0, -meter.BuildLogRetentionDays)) {
		return false, nil
	}
	newest, err := store.ListDeploymentsForApp(ctx, d.AppID, api.BuildLogRetainedDeployments, 0)
	if err != nil {
		return false, fmt.Errorf("buildlog: list newest deployments: %w", err)
	}
	for _, n := range newest {
		if n.ID == d.ID {
			return false, nil
		}
	}
	return true, nil
}

// Notice is the data of the api.DeploymentLogExpiredEvent frame.
func Notice() api.DeploymentLogExpiredNotice {
	return api.DeploymentLogExpiredNotice{
		Message: fmt.Sprintf("This deployment's build log has expired. Build logs are deleted %d days after the build, "+
			"except for an app's live deployments and its %d most recent deployments.",
			meter.BuildLogRetentionDays, api.BuildLogRetainedDeployments),
		RetentionDays:       meter.BuildLogRetentionDays,
		RetainedDeployments: api.BuildLogRetainedDeployments,
	}
}
