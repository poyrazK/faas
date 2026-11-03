package issues

import (
	"context"
	"errors"
)

// DeliverOTLPGroups attempts one atomic export first, then isolates permanent
// failures by log record or span. A transient error remains retryable: callers
// must retain event identities when retrying already committed groups.
func DeliverOTLPGroups[T any, R any](ctx context.Context, groups [][]T, deliver func(context.Context, []T) ([]R, error), permanent func(error) bool, splittable func(error) bool, onReject func(error)) ([]R, int, error) {
	var batch []T
	for _, group := range groups {
		batch = append(batch, group...)
	}
	results, err := deliver(ctx, batch)
	var full interface{ IssueBatchNoCapacityAtStart() bool }
	if err != nil && errors.As(err, &full) && full.IssueBatchNoCapacityAtStart() {
		return results, 0, err
	}
	if err == nil || (!permanent(err) && !splittable(err)) {
		return results, 0, err
	}
	var accepted []R
	rejected := 0
	var retryErr error
	for _, group := range groups {
		results, err = deliver(ctx, group)
		if err != nil {
			if permanent(err) {
				rejected++
				if onReject != nil {
					onReject(err)
				}
				continue
			}
			if splittable(err) {
				// Keep trying independent records, including duplicates, but
				// return the transient error so the exporter retries the batch.
				if retryErr == nil {
					retryErr = err
				}
				continue
			}
			return accepted, rejected, err
		}
		accepted = append(accepted, results...)
	}
	return accepted, rejected, retryErr
}
