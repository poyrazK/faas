package gateway

import (
	"context"
	"hash/fnv"
	"sync"
)

// InstalledWeightsObserver receives exact database rows only after the picker
// installs them. It confirms cache application, never request drain.
type InstalledWeightsObserver interface {
	DeploymentWeightsInstalled(context.Context, string, []DeploymentWeightsRow) error
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
