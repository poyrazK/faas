package state

import (
	"context"
	"errors"
	"fmt"
)

// ErrTrafficSplitReleaseLimit refuses a manual traffic change that would give
// traffic to more releases than the app can run at once
// (api.TrafficSplitMaxReleases). The caller unwraps *TrafficSplitReleaseLimitError
// for the limit and the requested count.
var ErrTrafficSplitReleaseLimit = errors.New("state: traffic split names more releases than the app can run")

// TrafficSplitReleaseLimitError carries the limit and the number of releases
// the refused change would have given traffic.
type TrafficSplitReleaseLimitError struct {
	Limit     int
	Requested int
}

func (e *TrafficSplitReleaseLimitError) Error() string {
	return fmt.Sprintf("%v: %d releases would serve, the app runs at most %d", ErrTrafficSplitReleaseLimit, e.Requested, e.Limit)
}

func (e *TrafficSplitReleaseLimitError) Unwrap() error { return ErrTrafficSplitReleaseLimit }

type trafficReleaseLimitKey struct{}

// WithTrafficSplitReleaseLimit makes UpdateDeploymentTraffic refuse a change
// that gives traffic to more than limit releases of the app. apid sets it
// from the app's effective concurrency; a limit of 0 or less sets nothing.
func WithTrafficSplitReleaseLimit(ctx context.Context, limit int) context.Context {
	if limit <= 0 {
		return ctx
	}
	return context.WithValue(ctx, trafficReleaseLimitKey{}, limit)
}

// checkTrafficSplitReleaseLimit compares the releases serving before and after
// a manual traffic change. A change that does not add a serving release is
// always allowed, so a split already over the limit can still be narrowed.
func checkTrafficSplitReleaseLimit(ctx context.Context, priorTarget, nextTarget int, prior, next []int) error {
	limit, ok := ctx.Value(trafficReleaseLimitKey{}).(int)
	if !ok || limit <= 0 {
		return nil
	}
	serving := func(target int, siblings []int) int {
		count := 0
		if target > 0 {
			count++
		}
		for _, weight := range siblings {
			if weight > 0 {
				count++
			}
		}
		return count
	}
	before, after := serving(priorTarget, prior), serving(nextTarget, next)
	if after <= limit || after <= before {
		return nil
	}
	return &TrafficSplitReleaseLimitError{Limit: limit, Requested: after}
}
