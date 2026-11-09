package state

import (
	"context"
	"fmt"
)

type expectedTrafficWeightsKey struct{}

// WithExpectedTrafficWeights makes a manual traffic change conditional on the
// split it was planned from: every live release in the target's environment
// with positive traffic, and its percent. The store compares it with the rows
// it holds locked, so a change made between the caller's read and its write
// (another split, a promotion, a rollback) fails with
// ErrTrafficServingChanged instead of being overwritten (storage review
// finding #2 on traffic rollback). A nil map leaves the change unconditional.
func WithExpectedTrafficWeights(ctx context.Context, weights map[string]int) context.Context {
	if weights == nil {
		return ctx
	}
	return context.WithValue(ctx, expectedTrafficWeightsKey{}, weights)
}

// checkExpectedTrafficWeights compares the expected split with the current
// one: current maps every live release in scope, the target included, to its
// percent. Releases at 0% are not part of a split and are ignored on both
// sides.
func checkExpectedTrafficWeights(ctx context.Context, current map[string]int) error {
	expected, ok := ctx.Value(expectedTrafficWeightsKey{}).(map[string]int)
	if !ok {
		return nil
	}
	serving := 0
	for id, percent := range current {
		if percent <= 0 {
			continue
		}
		serving++
		if !expectedWeight(expected, id, percent) {
			return fmt.Errorf("state: release %s serves %d%%, not as expected: %w", id, percent, ErrTrafficServingChanged)
		}
	}
	want := 0
	for _, percent := range expected {
		if percent > 0 {
			want++
		}
	}
	if want != serving {
		return fmt.Errorf("state: %d releases serve traffic, expected %d: %w", serving, want, ErrTrafficServingChanged)
	}
	return nil
}

func expectedWeight(expected map[string]int, id string, percent int) bool {
	if got, ok := expected[id]; ok {
		return got == percent
	}
	for candidate, got := range expected {
		if sameDeploymentID(candidate, id) {
			return got == percent
		}
	}
	return false
}
