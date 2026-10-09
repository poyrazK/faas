package gateway

import (
	"context"
	"hash/fnv"
	"sync"
	"time"
)

// InstalledWeightsObserver receives exact database rows only after the picker
// installs them. It confirms cache application, never request drain.
type InstalledWeightsObserver interface {
	DeploymentWeightsInstalled(context.Context, string, []DeploymentWeightsRow) error
}

// The private drain projection binds exact weights and routing tokens from one
// database snapshot. Publication remains under the same refresh stripe.
type DeploymentWeightsSnapshot struct {
	Rows            []DeploymentWeightsRow
	RoutingRevision string
	Drain           *RuntimeUpgradeDrainPlan
}

type RuntimeUpgradeDrainPlan struct {
	OperationID, DeploymentID, ServingDeploymentID, GatewayRosterRevision string
	CutoverAt                                                             time.Time
}

type DeploymentWeightsSnapshotStore interface {
	DeploymentWeightsSnapshot(context.Context, string) (DeploymentWeightsSnapshot, error)
}

type InstalledWeightsSnapshotObserver interface {
	DeploymentWeightsSnapshotInstalled(context.Context, string, DeploymentWeightsSnapshot) error
}

func readDeploymentWeightsSnapshot(ctx context.Context, store deploymentWeightsStore, appID string) (DeploymentWeightsSnapshot, error) {
	if source, ok := store.(DeploymentWeightsSnapshotStore); ok {
		return source.DeploymentWeightsSnapshot(ctx, appID)
	}
	rows, err := store.LiveDeployments(ctx, appID)
	return DeploymentWeightsSnapshot{Rows: rows}, err
}

type weightsRefreshLock struct {
	once  sync.Once
	token chan struct{}
}

func (b *PGBackend) lockWeightsRefresh(ctx context.Context, appID string) (func(), error) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(appID))
	lock := &b.weightsRefresh[h.Sum32()%uint32(len(b.weightsRefresh))]
	lock.once.Do(func() { lock.token = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case lock.token <- struct{}{}:
		return func() { <-lock.token }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
