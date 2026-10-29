package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261024222102 — meterd attaches the alert log context only while
// FAAS_LOG_SEARCH_ENABLED is on and the log-search capability flag is
// released to the firing alert's account.

type logSearchReleaseFake struct {
	accounts map[string]state.Account
	apps     map[string]state.App
}

func (f logSearchReleaseFake) AccountByID(_ context.Context, id string) (state.Account, error) {
	if acct, ok := f.accounts[id]; ok {
		return acct, nil
	}
	return state.Account{}, state.ErrNotFound
}

func (f logSearchReleaseFake) AppByID(_ context.Context, id string) (state.App, error) {
	if app, ok := f.apps[id]; ok {
		return app, nil
	}
	return state.App{}, state.ErrNotFound
}

func TestLogSearchReleaseFollowsSwitchAndCapability(t *testing.T) {
	yes := true
	pro := state.Account{ID: "00000000-0000-4000-8000-000000000001", Plan: api.PlanPro}
	hobby := state.Account{ID: "00000000-0000-4000-8000-000000000002", Plan: api.PlanHobby}
	gone := "00000000-0000-4000-8000-000000000009"
	store := logSearchReleaseFake{
		accounts: map[string]state.Account{pro.ID: pro, hobby.ID: hobby},
		apps:     map[string]state.App{"app-pro": {ID: "app-pro", AccountID: pro.ID}, "app-hobby": {ID: "app-hobby", AccountID: hobby.ID}},
	}
	proOnly := platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{{ID: "pro", Plans: []string{string(api.PlanPro)}, Value: &yes}}}
	type check struct {
		account, app string
		want         bool
	}
	for _, tc := range []struct {
		name    string
		env     string
		written *platformflags.Definition
		checks  []check
	}{
		{name: "the operator switch off keeps every account off", env: "", written: &proOnly,
			checks: []check{{pro.ID, "app-pro", false}}},
		{name: "an internal capability is off for every account until released", env: "1",
			checks: []check{{pro.ID, "app-pro", false}, {hobby.ID, "app-hobby", false}}},
		{name: "a written plan rule releases the accounts it names", env: "1", written: &proOnly,
			checks: []check{{pro.ID, "app-pro", true}, {hobby.ID, "app-hobby", false}}},
		{name: "an unreadable account decides off", env: "1", written: &proOnly,
			checks: []check{{gone, "app-pro", false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := platformflags.NewSet(nil)
			if tc.written != nil {
				key := platformflags.ConfigKey(platformflags.CapabilityFlagKey(logSearchCapability))
				if err := flags.Apply(key, tc.written.Marshal(), 1); err != nil {
					t.Fatal(err)
				}
			}
			release, err := newLogSearchRelease(func(key string) string {
				if key == "FAAS_LOG_SEARCH_ENABLED" {
					return tc.env
				}
				return ""
			}, flags, store)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range tc.checks {
				if got := release.Released(context.Background(), c.account, c.app); got != c.want {
					t.Fatalf("account %s app %s = %v, want %v", c.account, c.app, got, c.want)
				}
			}
		})
	}
}
