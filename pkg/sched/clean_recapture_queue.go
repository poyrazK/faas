package sched

// The snapshot refresher's clean recapture queue (ADR-20261115113000). A park
// that found no init snapshot on its node queues its app and node here; Run
// drains the queue through the refresher's own boot path, so a recapture is
// admitted, booted, primed, captured and parked exactly as a release refresh
// is, one at a time, only while the node keeps its headroom.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// cleanRecaptureInterval is how often Run retries queued recaptures that
// were deferred or are backing off; a new request wakes it at once.
const cleanRecaptureInterval = 30 * time.Second

// SetCleanRecapture applies `snapshot_clean_recapture`.
func (r *SnapshotRefresher) SetCleanRecapture(mode string) error {
	if !api.ValidSnapshotCleanRecapture(mode) {
		return fmt.Errorf("sched: snapshot_clean_recapture: %q is not off or on", mode)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanMode = mode
	if mode == api.SnapshotCleanRecaptureOff {
		r.recaptures = map[string]*cleanRecaptureRequest{}
	}
	return nil
}

// CleanRecaptureEnabled implements cleanRecaptureSink.
func (r *SnapshotRefresher) CleanRecaptureEnabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cleanMode == api.SnapshotCleanRecaptureOn
}

// RequestCleanRecapture implements cleanRecaptureSink. A second request for
// the same app and node replaces the deployment and keeps the backoff.
func (r *SnapshotRefresher) RequestCleanRecapture(appID, deploymentID, nodeID string) {
	r.mu.Lock()
	key := cleanRecaptureKey(appID, nodeID)
	if req, ok := r.recaptures[key]; ok {
		req.deploymentID = deploymentID
	} else {
		r.recaptures[key] = &cleanRecaptureRequest{appID: appID, deploymentID: deploymentID, nodeID: nodeID}
	}
	r.mu.Unlock()
	r.metrics.recapture(CleanRecaptureQueued)
	select {
	case r.recaptureC <- struct{}{}:
	default:
	}
}

// PendingCleanRecaptures is the number of queued recaptures.
func (r *SnapshotRefresher) PendingCleanRecaptures() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.recaptures)
}

// DrainCleanRecaptures runs every due request once, oldest app first. A
// request stays queued when it was deferred or failed with attempts left.
func (r *SnapshotRefresher) DrainCleanRecaptures(ctx context.Context) {
	for _, req := range r.dueRecaptures() {
		if ctx.Err() != nil {
			return
		}
		outcome := r.cleanRecapture(ctx, req)
		r.metrics.recapture(outcome)
		r.settleRecapture(req, outcome)
	}
}

func (r *SnapshotRefresher) dueRecaptures() []cleanRecaptureRequest {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	due := make([]cleanRecaptureRequest, 0, len(r.recaptures))
	for _, req := range r.recaptures {
		if !now.Before(req.notBefore) {
			due = append(due, *req)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		return cleanRecaptureKey(due[i].appID, due[i].nodeID) < cleanRecaptureKey(due[j].appID, due[j].nodeID)
	})
	return due
}

// settleRecapture removes a finished request or schedules its retry. A park
// that requeued it meanwhile with another deployment keeps its new target.
func (r *SnapshotRefresher) settleRecapture(done cleanRecaptureRequest, outcome string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := cleanRecaptureKey(done.appID, done.nodeID)
	req, ok := r.recaptures[key]
	if !ok {
		return
	}
	switch outcome {
	case CleanRecaptureRefreshed, CleanRecaptureDropped:
		if req.deploymentID == done.deploymentID {
			delete(r.recaptures, key)
		}
	case CleanRecaptureFailed:
		req.attempts++
		if req.attempts >= api.SnapshotCleanRecaptureMaxAttempts {
			delete(r.recaptures, key)
			r.log.Warn("snapshot clean recapture: giving up; the app's next park asks again",
				"app_id", req.appID, "node_id", req.nodeID, "deployment_id", req.deploymentID, "attempts", req.attempts)
			return
		}
		req.notBefore = r.now().Add(cleanRecaptureBackoff(req.attempts))
	}
}

// cleanRecapture boots one queued request through the refresh path.
func (r *SnapshotRefresher) cleanRecapture(ctx context.Context, req cleanRecaptureRequest) string {
	e := r.e
	var t refreshTarget
	var err error
	t.app, t.acct, t.limits, err = e.resolveAppAccount(ctx, req.appID)
	if err != nil {
		r.log.Info("snapshot clean recapture: app lookup", "app_id", req.appID, "err", err)
		return CleanRecaptureDropped
	}
	if !e.ownsApp(t.app) {
		return CleanRecaptureDropped
	}
	var reason string
	if t.dep, reason = r.refreshableDeployment(ctx, t.app, req.deploymentID); reason != "" {
		r.log.Info("snapshot clean recapture: not eligible", "app_id", req.appID, "deployment_id", req.deploymentID, "reason", reason)
		return CleanRecaptureDropped
	}
	if _, ok := e.reusableSnapshotOn(ctx, t.dep.ID, state.SnapshotTierInit, req.nodeID); ok {
		// Another path (a deploy prime, a release refresh) captured one.
		return CleanRecaptureDropped
	}
	switch outcome := r.refresh(ctx, req.nodeID, t); outcome {
	case SnapshotRefreshRefreshed:
		return CleanRecaptureRefreshed
	case SnapshotRefreshDeferred, SnapshotRefreshSkippedLive:
		return CleanRecaptureDeferred
	default:
		return CleanRecaptureFailed
	}
}

func (m *snapshotRefreshMetrics) recapture(o string) { m.recaptures.WithLabelValues(o).Inc() }
