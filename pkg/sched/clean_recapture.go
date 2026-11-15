package sched

// Clean init snapshot recapture (ADR-20261115113000). A deployment's init
// snapshot is captured right after readiness (and priming) by the deploy
// prime and by the release refresher. When a wake had to cold-boot because no
// usable init snapshot was on its node (a cache eviction, a node that never
// held it, a refresh that did not run), the next park used to capture that
// instance instead: a snapshot holding whatever the requests it served left in
// memory (sessions, caches, /tmp), restored into every later wake. Now that
// park destroys the instance and asks the refresher for a clean recapture: a
// fresh boot on the same node, readiness, priming, capture, park. Wakes before
// it lands cold-boot (ADR-005: snapshots are cache).

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Clean recapture outcomes (schedd_init_snapshot_clean_recapture_total).
const (
	// CleanRecaptureQueued: a park destroyed its instance instead of
	// capturing it and queued a clean recapture.
	CleanRecaptureQueued = "queued"
	// CleanRecaptureRefreshed: the clean boot captured and parked.
	CleanRecaptureRefreshed = "refreshed"
	// CleanRecaptureDeferred: the node lacked headroom, or the app had a live
	// instance; the request stays queued.
	CleanRecaptureDeferred = "deferred"
	// CleanRecaptureFailed: the clean boot or capture failed; retried with
	// backoff up to api.CleanRecaptureMaxAttempts.
	CleanRecaptureFailed = "failed"
	// CleanRecaptureDropped: no longer needed or no longer possible (a
	// snapshot appeared, the deployment is no longer live, the app is
	// ineligible, or the attempts ran out). The app's next park asks again.
	CleanRecaptureDropped = "dropped"
)

var cleanRecaptureOutcomes = []string{
	CleanRecaptureQueued, CleanRecaptureRefreshed, CleanRecaptureDeferred,
	CleanRecaptureFailed, CleanRecaptureDropped,
}

// cleanRecaptureSink receives the parks that need a clean recapture. The
// snapshot refresher implements it; an engine without one captures at park
// as before.
type cleanRecaptureSink interface {
	CleanRecaptureEnabled() bool
	RequestCleanRecapture(appID, deploymentID, nodeID string)
}

// parkNeedsCleanRecapture reports whether this park would capture an init
// snapshot from an instance that served requests, and a clean recapture can
// replace it. Profiled and frozen-function parks keep their capture (they
// carry a checkpoint handshake), sealed-grant (measured) runtimes never reuse
// a capture, and only request-mode apps idle-park and refresh.
func (e *Engine) parkNeedsCleanRecapture(ctx context.Context, ins state.Instance, app state.App, profilingEnabled, frozenFunction bool) bool {
	sink := e.cleanRecapture
	if sink == nil || !sink.CleanRecaptureEnabled() || ins.DeploymentID == "" ||
		profilingEnabled || frozenFunction || executionModeForApp(app) != api.ExecutionModeRequest {
		return false
	}
	if state.IsRefresh(ins.Mode) {
		return false
	}
	if _, ok := e.reusableSnapshotOn(ctx, ins.DeploymentID, state.SnapshotTierInit, ins.NodeID); ok {
		return false
	}
	measured, err := e.measuredSnapshotSource(ctx, ins)
	return err == nil && !measured
}

// parkForCleanRecapture retires the instance without capturing it and queues
// the clean recapture. An uncertain destroy keeps the row and its
// reservation, as the other discard-at-park paths do; the next park retries.
func (e *Engine) parkForCleanRecapture(ctx context.Context, ins state.Instance) error {
	if err := e.timedDestroy(context.WithoutCancel(ctx), ins.NodeID, ins.ID, DestroyTimeout); err != nil {
		return fmt.Errorf("sched: park: destroy instance %s for a clean recapture: %w", ins.ID, err)
	}
	e.ledger.Release(ins.ID)
	e.transitionWithKind(ctx, ins.ID, ins.AppID, state.StateStopped, "park_clean_recapture", "init_snapshot_missing")
	e.log.Info("sched: park: no init snapshot on node; queued a clean recapture instead of capturing a used instance",
		"instance", ins.ID, "app", ins.AppID, "deployment", ins.DeploymentID, "node", ins.NodeID)
	e.cleanRecapture.RequestCleanRecapture(ins.AppID, ins.DeploymentID, ins.NodeID)
	return nil
}

// cleanRecaptureRequest is one queued recapture, keyed by app and node.
type cleanRecaptureRequest struct {
	appID, deploymentID, nodeID string
	attempts                    int
	notBefore                   time.Time
}

func cleanRecaptureKey(appID, nodeID string) string { return appID + "\x00" + nodeID }

// cleanRecaptureBackoff is the wait before attempt n+1 after n failures.
func cleanRecaptureBackoff(failures int) time.Duration {
	switch {
	case failures <= 0:
		return 0
	case failures == 1:
		return time.Minute
	case failures == 2:
		return 5 * time.Minute
	default:
		return 15 * time.Minute
	}
}
