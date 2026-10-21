package deploystaging

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

type ProjectRetryStore interface {
	AppByID(context.Context, string) (state.App, error)
	LatestDeployment(context.Context, string) (state.Deployment, error)
}

// ValidateProjectRetry rejects replaced apps, newer attempts and non-failures.
// Comparing the latest attempt also keeps a repeated retry from replacing work
// that was already accepted by an earlier retry.
func ValidateProjectRetry(ctx context.Context, store ProjectRetryStore, accountID, projectID, environment string, only map[string]bool, targets map[string]string) error {
	if len(targets) == 0 || len(only) != len(targets) {
		return errors.New("retry requires exactly the failed workloads in --only")
	}
	for appID, deploymentID := range targets {
		app, err := store.AppByID(ctx, appID)
		if err != nil {
			return fmt.Errorf("read retry app: %w", err)
		}
		if app.AccountID != accountID || app.ProjectID != projectID || app.Status == state.AppDeleted || !only[strings.ToLower(app.WorkloadName)] {
			return errors.New("retry workload identity changed")
		}
		latest, err := store.LatestDeployment(ctx, appID)
		if err != nil {
			return fmt.Errorf("read latest retry deployment: %w", err)
		}
		scope := environment
		if scope == "" || scope == "production" {
			scope = state.DefaultEnvScope
		}
		latestScope := latest.Scope
		if latestScope == "" {
			latestScope = state.DefaultEnvScope
		}
		if latest.ID != deploymentID || latest.Status != state.DeployFailed || latestScope != scope {
			return errors.New("retry target is no longer the latest failed deployment")
		}
	}
	return nil
}
