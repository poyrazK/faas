package environmentgitops

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type RetirementNotifier interface {
	Notify(context.Context, string, string) error
	Subscribe(context.Context, []string) (<-chan db.Notification, func(), error)
}

// ReconcileWorkloadRetirements retries withdrawal from durable generations.
// A notification or a lost commit reply is never treated as proof of drain.
func ReconcileWorkloadRetirements(ctx context.Context, store state.EnvironmentGitOpsWorkloadRetirementStore,
	notifier RetirementNotifier, lease state.EnvironmentGitOpsLease, nodes []string, timeout, retryEvery time.Duration) (bool, error) {
	rows, err := store.PendingEnvironmentGitOpsWorkloadRetirements(ctx, lease, nodes)
	if err != nil {
		return false, err
	}
	pending := map[int64]state.EnvironmentWorkloadRetirement{}
	for _, r := range rows {
		if r.RouteDeploymentID != "" && !slices.Equal(r.ExpectedGateways, r.AcknowledgedGateways) {
			pending[r.RouteGeneration] = r
		}
	}
	if len(pending) == 0 {
		return store.ReconcileEnvironmentGitOpsWorkloadRetirements(ctx, lease)
	}
	if timeout <= 0 || retryEvery <= 0 {
		return false, state.ErrInvalidArgument
	}
	events, cancel, err := notifier.Subscribe(ctx, []string{db.NotifyDeploymentRouteAck})
	if err != nil {
		return false, err
	}
	defer cancel()
	waitCtx, stop := context.WithTimeout(ctx, timeout)
	defer stop()
	retry := time.NewTicker(retryEvery)
	defer retry.Stop()
	notify := func() error {
		for _, r := range pending {
			payload, err := json.Marshal(db.DeploymentRouteChangedPayload{AppID: r.AppID, DeploymentID: r.RouteDeploymentID, Generation: r.RouteGeneration})
			if err != nil {
				return err
			}
			if err := notifier.Notify(waitCtx, db.NotifyDeploymentRouteChanged, string(payload)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := notify(); err != nil {
		return false, err
	}
	for {
		select {
		case <-waitCtx.Done():
			return false, ctx.Err()
		case event, open := <-events:
			if !open {
				return false, ctx.Err()
			}
			ack, err := db.ParseDeploymentRouteAckPayload(event.Payload)
			if err != nil {
				continue
			}
			r, ok := pending[ack.Generation]
			if !ok || !slices.Contains(r.ExpectedGateways, ack.Node) {
				continue
			}
			if err := store.AcknowledgeEnvironmentGitOpsWorkloadRetirement(waitCtx, lease, r.ID, r.RouteGeneration, ack.Node); err != nil {
				if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
					continue
				}
				return false, err
			}
			if !slices.Contains(r.AcknowledgedGateways, ack.Node) {
				r.AcknowledgedGateways = append(r.AcknowledgedGateways, ack.Node)
				slices.Sort(r.AcknowledgedGateways)
			}
			pending[r.RouteGeneration] = r
			if slices.Equal(r.ExpectedGateways, r.AcknowledgedGateways) {
				delete(pending, r.RouteGeneration)
			}
			if len(pending) == 0 {
				return store.ReconcileEnvironmentGitOpsWorkloadRetirements(ctx, lease)
			}
		case <-retry.C:
			if err := notify(); err != nil {
				return false, err
			}
		}
	}
}
