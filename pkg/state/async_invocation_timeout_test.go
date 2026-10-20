package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type countingAccounts struct {
	plan  api.Plan
	err   error
	calls int
}

func (c *countingAccounts) AccountByID(context.Context, string) (state.Account, error) {
	c.calls++
	return state.Account{Plan: c.plan}, c.err
}

// ADR-1180: an app's async timeout follows its request timeout within its
// plan, and an app at the default never pays for an account lookup.
func TestAsyncInvocationTimeout(t *testing.T) {
	for _, tc := range []struct {
		name            string
		requestTimeoutS int
		accounts        *countingAccounts
		want            time.Duration
		wantLookups     int
	}{
		{name: "default app", accounts: &countingAccounts{plan: api.PlanPro}, want: api.AsyncInvocationTimeoutFloor},
		{name: "30 s app", requestTimeoutS: 30, accounts: &countingAccounts{plan: api.PlanPro}, want: api.AsyncInvocationTimeoutFloor},
		{name: "pro two minutes", requestTimeoutS: 120, accounts: &countingAccounts{plan: api.PlanPro}, want: 2 * time.Minute, wantLookups: 1},
		{name: "hobby clamped", requestTimeoutS: 600, accounts: &countingAccounts{plan: api.PlanHobby}, want: time.Minute, wantLookups: 1},
		{name: "lookup failure", requestTimeoutS: 600, accounts: &countingAccounts{err: errors.New("down")}, want: api.AsyncInvocationTimeoutFloor, wantLookups: 1},
	} {
		app := state.App{AccountID: "acct", Manifest: state.AppManifest{RequestTimeoutS: tc.requestTimeoutS}}
		if got := state.AsyncInvocationTimeout(context.Background(), tc.accounts, app); got != tc.want {
			t.Errorf("%s: timeout %s, want %s", tc.name, got, tc.want)
		}
		if tc.accounts.calls != tc.wantLookups {
			t.Errorf("%s: %d account lookups, want %d", tc.name, tc.accounts.calls, tc.wantLookups)
		}
	}
}
