package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AccountReader is the account lookup AsyncInvocationTimeout needs.
type AccountReader interface {
	AccountByID(ctx context.Context, id string) (Account, error)
}

// AsyncInvocationTimeout is how long one async, queue, cron or replay
// invocation of app may run (ADR-1180): the app's request timeout within its
// plan's ceiling, never less than api.AsyncInvocationTimeoutFloor. Only an
// app whose own timeout is above the floor costs an account lookup, and a
// failed lookup keeps the floor every invocation had before.
func AsyncInvocationTimeout(ctx context.Context, accounts AccountReader, app App) time.Duration {
	requestTimeoutS := app.Manifest.RequestTimeoutS
	if time.Duration(requestTimeoutS)*time.Second <= api.AsyncInvocationTimeoutFloor || accounts == nil {
		return api.AsyncInvocationTimeoutFloor
	}
	acct, err := accounts.AccountByID(ctx, app.AccountID)
	if err != nil {
		return api.AsyncInvocationTimeoutFloor
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return api.AsyncInvocationTimeoutFloor
	}
	return limits.AsyncInvocationTimeout(requestTimeoutS)
}
