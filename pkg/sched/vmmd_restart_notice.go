package sched

// The planned-restart notice (vmmd restart adoption ADR §3). A vmmd that
// adopts on restart sets compute_nodes.vmmd_restart_notice_until as it stops
// and clears it once its successor serves. While a node's notice is active:
//
//   - placement tries the other nodes first;
//   - a create, destroy, job-exit wait or held-job release that finds the
//     node's vmmd unavailable is retried until the notice ends instead of
//     failing (VMMRouter);
//   - the heartbeat counts no failed pings against the node.
//
// A crash sets no notice, so none of this applies to it.

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/state"
)

// restartNoticeRefresh bounds how stale the cached notices are. A notice
// lasts api.VMMDRestartNoticeSeconds, so a second of lag is noise.
const restartNoticeRefresh = time.Second

// restartNoticeConfirmAge bounds the cache for a call that just failed: a
// notice set less than restartNoticeRefresh ago must still turn the failure
// into a retry.
const restartNoticeConfirmAge = 100 * time.Millisecond

// restartNoticeRetryEvery is how often a create retries a restarting vmmd.
const restartNoticeRetryEvery = 250 * time.Millisecond

// RestartNotices caches the active notices. The zero value and nil are
// usable and report none.
type RestartNotices struct {
	store state.VMMDRestartNoticeStore
	now   func() time.Time

	mu      sync.Mutex
	fetched time.Time
	until   map[string]time.Time
}

// NewRestartNotices reads notices from store, or returns nil when the store
// keeps none.
func NewRestartNotices(store state.Store) *RestartNotices {
	s, ok := store.(state.VMMDRestartNoticeStore)
	if !ok {
		return nil
	}
	return &RestartNotices{store: s, now: time.Now}
}

// Active reports whether nodeID's vmmd is restarting under a notice. A
// failed read reports none: without the notice, today's behaviour applies.
func (n *RestartNotices) Active(ctx context.Context, nodeID string) bool {
	return n.active(ctx, nodeID, restartNoticeRefresh)
}

// confirm is Active for a call that just failed: it re-reads a cache older
// than restartNoticeConfirmAge.
func (n *RestartNotices) confirm(ctx context.Context, nodeID string) bool {
	return n.active(ctx, nodeID, restartNoticeConfirmAge)
}

func (n *RestartNotices) active(ctx context.Context, nodeID string, maxAge time.Duration) bool {
	if n == nil || n.store == nil || nodeID == "" {
		return false
	}
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.until == nil || now.Sub(n.fetched) >= maxAge {
		if fresh, err := n.store.ListComputeNodeVMMDRestartNotices(ctx); err == nil {
			n.until, n.fetched = fresh, now
		} else if n.until == nil {
			return false
		}
	}
	until, ok := n.until[nodeID]
	return ok && now.Before(until)
}

// withRestartNoticeRetry runs call, and while it fails because nodeID's vmmd
// is unavailable under an active notice, waits and runs it again. The wake
// hold bounds the wait through ctx.
func withRestartNoticeRetry[T any](ctx context.Context, notices *RestartNotices, nodeID string, call func() (T, error)) (T, error) {
	for {
		out, err := call()
		if err == nil || !vmmUnavailable(err) {
			return out, err
		}
		if !notices.confirm(ctx, nodeID) {
			return out, err
		}
		timer := time.NewTimer(restartNoticeRetryEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return out, err
		case <-timer.C:
		}
	}
}

// vmmUnavailable reports a vmmd that could not be reached: a refused or
// broken connection, which the router surfaces as gRPC UNAVAILABLE.
func vmmUnavailable(err error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if status.Code(e) == codes.Unavailable {
			return true
		}
	}
	return false
}

// preferUnnoticed returns the nodes without an active notice when that
// leaves any, so placement tries them first; otherwise all of them.
func preferUnnoticed(ctx context.Context, notices *RestartNotices, nodes []state.ComputeNode) ([]state.ComputeNode, bool) {
	if notices == nil {
		return nodes, false
	}
	kept := make([]state.ComputeNode, 0, len(nodes))
	for _, n := range nodes {
		if !notices.Active(ctx, n.ID) {
			kept = append(kept, n)
		}
	}
	if len(kept) == 0 || len(kept) == len(nodes) {
		return nodes, false
	}
	return kept, true
}
