package main

import (
	"context"
	"errors"
	"time"
)

func validateListTimeout(duration time.Duration, selected bool) error {
	if selected && duration <= 0 {
		return errors.New("--timeout requires a positive duration, such as 30s or 2m")
	}
	return nil
}

// The omitted option retains the caller's context and its existing defaults.
// One deadline is shared by enrichment, pages, retries and publication checks.
func listReadContext(parent context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
	if duration == 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, duration)
}

// A successful terminal read must still finish within its overall budget.
// Preserve actual API/output errors (and their request IDs) if one occurred.
func listReadError(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	return ctx.Err()
}
