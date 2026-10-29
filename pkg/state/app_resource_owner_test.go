package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestSameAppResourceOwner(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b state.App
		want bool
	}{
		{"same company different namespaces", state.App{AccountID: "alice", OrgID: "company"}, state.App{AccountID: "bob", OrgID: "company"}, true},
		{"same namespace different companies", state.App{AccountID: "alice", OrgID: "one"}, state.App{AccountID: "alice", OrgID: "two"}, false},
		{"personal and company", state.App{AccountID: "alice"}, state.App{AccountID: "alice", OrgID: "company"}, false},
		{"legacy personal", state.App{AccountID: "alice"}, state.App{AccountID: "alice"}, true},
		{"missing namespace", state.App{OrgID: "company"}, state.App{AccountID: "bob", OrgID: "company"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := state.SameAppResourceOwner(tc.a, tc.b); got != tc.want {
				t.Fatalf("ownership=%v; want %v", got, tc.want)
			}
		})
	}
}

type ownerReader struct {
	account state.Account
	org     state.Org
}

func (o ownerReader) AccountByID(context.Context, string) (state.Account, error) {
	return o.account, nil
}
func (o ownerReader) OrgByID(context.Context, string) (state.Org, error) { return o.org, nil }

// Before billing adoption a company app's resource owner is its creator's
// account, carrying the company's status when that is worse. A past-due
// company keeps running but may not deploy (spec §4.7), so an active creator
// reads as past_due there, and a creator already suspended stays suspended.
func TestLegacyCompanyOwnerCarriesTheCompanysDunningStatus(t *testing.T) {
	for _, tc := range []struct {
		org     state.OrgStatus
		creator state.AccountStatus
		want    state.AccountStatus
	}{
		{state.OrgStatusActive, state.AccountActive, state.AccountActive},
		{state.OrgStatusActive, state.AccountPastDue, state.AccountPastDue},
		{state.OrgStatusPastDue, state.AccountActive, state.AccountPastDue},
		{state.OrgStatusPastDue, state.AccountSuspended, state.AccountSuspended},
		{state.OrgStatusSuspended, state.AccountActive, state.AccountSuspended},
	} {
		t.Run(string(tc.org)+"/"+string(tc.creator), func(t *testing.T) {
			reader := ownerReader{
				account: state.Account{ID: "creator", Status: tc.creator},
				org:     state.Org{ID: "company", Status: tc.org},
			}
			owner, err := state.AppResourceAccount(t.Context(), reader, state.App{AccountID: "creator", OrgID: "company"})
			if err != nil || owner.Status != tc.want {
				t.Fatalf("owner status = %q, %v; want %q", owner.Status, err, tc.want)
			}
			if tc.want == state.AccountPastDue && (!owner.Active() || owner.MayDeploy()) {
				t.Fatalf("past-due owner Active=%v MayDeploy=%v; want running and not deploying", owner.Active(), owner.MayDeploy())
			}
		})
	}
}
