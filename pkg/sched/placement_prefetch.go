package sched

import "context"

// placementPrefetch loads a wake's placement snapshot (the active nodes and
// their usage) while the wake reads its snapshot inputs, which do not
// change it (ADR-1741). The first placement attempt uses it; a retry after a
// stale placement loads fresh.
type placementPrefetch struct {
	appID    string
	done     chan struct{}
	snapshot placementSnapshot
	err      error
}

type placementPrefetchKey struct{}

func (e *Engine) startPlacementPrefetch(ctx context.Context, r Request) *placementPrefetch {
	p := &placementPrefetch{appID: r.AppID, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		p.snapshot, p.err = e.loadPlacementSnapshotFresh(ctx, r)
	}()
	return p
}

func withPlacementPrefetch(ctx context.Context, p *placementPrefetch) context.Context {
	return context.WithValue(ctx, placementPrefetchKey{}, p)
}

// prefetchedPlacement returns the prefetched snapshot for r's app, once.
func prefetchedPlacement(ctx context.Context, r Request) (placementSnapshot, error, bool) {
	p, ok := ctx.Value(placementPrefetchKey{}).(*placementPrefetch)
	if !ok || p == nil || p.appID != r.AppID || freshNodePlacement(ctx) {
		return placementSnapshot{}, nil, false
	}
	<-p.done
	return p.snapshot, p.err, true
}
