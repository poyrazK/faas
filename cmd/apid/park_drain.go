package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// The park endpoint is the zero-live-instance boundary used by callers
	// before issuing a wake. Five seconds covers the normal reaper/snapshot
	// drain while still returning a bounded retryable error if schedd is
	// unavailable.
	appParkDrainTimeout = 5 * time.Second
	appParkDrainPoll    = 50 * time.Millisecond
)

// activeInstancesLister is implemented by PgStore and MemStore. Keeping it
// optional lets focused handler test doubles continue to implement the
// smaller Store interface while production uses the bounded SQL query.
type activeInstancesLister interface {
	ListActiveInstancesForApp(context.Context, string, int) ([]state.Instance, error)
}

// waitForAppInstancesDrained establishes the park completion boundary. The
// scheduler still owns instance state transitions; apid only observes the
// live-state set and waits until no resident instance can serve traffic.
func waitForAppInstancesDrained(ctx context.Context, store state.Store, appID string, timeout, poll time.Duration) error {
	if timeout <= 0 {
		timeout = appParkDrainTimeout
	}
	if poll <= 0 {
		poll = appParkDrainPoll
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		var (
			instances []state.Instance
			err       error
		)
		if lister, ok := store.(activeInstancesLister); ok {
			// One row is sufficient to answer the drain question and keeps
			// this path independent of parked instance history.
			instances, err = lister.ListActiveInstancesForApp(waitCtx, appID, 1)
		} else {
			instances, err = store.ListInstancesForApp(waitCtx, appID)
		}
		if err != nil {
			return err
		}
		live := false
		for _, instance := range instances {
			if state.IsLive(instance.State) {
				live = true
				break
			}
		}
		if !live {
			return nil
		}

		timer := time.NewTimer(poll)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return waitCtx.Err()
		case <-timer.C:
		}
	}
}

// transitionAppStatus uses the atomic store extension on every real store.
// The fallback preserves compatibility with narrow test doubles.
func transitionAppStatus(ctx context.Context, store state.Store, appID string, from, to state.AppStatus) (bool, error) {
	if atomicStore, ok := store.(appStatusCompareAndSetter); ok {
		return atomicStore.CompareAndSetAppStatus(ctx, appID, from, to)
	}
	st := to
	_, err := store.UpdateApp(ctx, appID, state.UpdateAppParams{Status: &st})
	return err == nil, err
}
