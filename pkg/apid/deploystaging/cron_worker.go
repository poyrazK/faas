package deploystaging

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

// RunCronActivationWorker keeps reconciling after transient failures. Durable
// guards make concurrent apid workers and process restarts safe.
func RunCronActivationWorker(ctx context.Context, store state.DeploymentCronActivationStore, changed func(context.Context, string, string, string), failed func()) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		changes, err := store.ReconcileDeploymentCrons(ctx)
		for _, change := range changes {
			if changed != nil {
				changed(ctx, change.AccountID, change.AppID, "activate")
			}
		}
		if err != nil && ctx.Err() == nil && failed != nil {
			failed()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
