package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/statusmirror"
)

// runStatusMirror pushes the /v1/status document to the off-platform mirror
// (ADR-20261115090000) every interval until ctx ends. Every apid replica
// pushes; the writes are idempotent and the newest wins, so the mirror keeps
// updating while any replica is alive. A failed push is logged and counted
// but never affects request serving.
func (s *server) runStatusMirror(ctx context.Context, store statusmirror.Store, publisher string, interval time.Duration) {
	if store == nil || interval <= 0 {
		return
	}
	push := func() {
		pushCtx, cancel := context.WithTimeout(ctx, interval)
		defer cancel()
		now := time.Now().UTC()
		err := statusmirror.PublishStatus(pushCtx, store, publisher, s.publicStatusOverview(pushCtx), now)
		s.statusMetrics.observeMirrorPublish(err, now)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("status: mirror push failed", "err", err)
		}
	}
	push()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			push()
		}
	}
}
