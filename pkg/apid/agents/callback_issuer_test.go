package agents_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeCallbackAccess is the issuer membership and the app owner a callback
// re-checks when used.
type fakeCallbackAccess struct {
	revision int64
	member   bool
	status   state.AccountStatus
}

func (f *fakeCallbackAccess) AccountByID(_ context.Context, id string) (state.Account, error) {
	return state.Account{ID: id, Plan: api.PlanPro, Status: f.status}, nil
}

func (f *fakeCallbackAccess) OrgByID(context.Context, string) (state.Org, error) {
	return state.Org{}, state.ErrNotFound
}

func (f *fakeCallbackAccess) AppByID(_ context.Context, id string) (state.App, error) {
	return state.App{ID: id, AccountID: callbackAccount, Status: state.AppActive}, nil
}

func (f *fakeCallbackAccess) OrgAccessSnapshot(context.Context, string, string) (state.OrgAccessSnapshot, error) {
	if !f.member {
		return state.OrgAccessSnapshot{}, state.ErrNotFound
	}
	return state.OrgAccessSnapshot{Role: state.OrgRoleAdmin, Revision: f.revision}, nil
}

// Bug hunt 34: a callback URL re-checked only its signature, expiry, thread
// revocation and endpoint. One issued by a company app's admin kept
// publishing into the agent's inbox for 24 hours after the admin was removed
// or demoted, or the app's owner was suspended.
func TestCallbackStopsWhenItsIssuerLosesAccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*fakeCallbackAccess)
	}{
		{"issuer removed from the organization", func(a *fakeCallbackAccess) { a.member = false }},
		{"issuer's role changed", func(a *fakeCallbackAccess) { a.revision++ }},
		{"app owner suspended", func(a *fakeCallbackAccess) { a.status = state.AccountSuspended }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCallbackFixture(t)
			access := &fakeCallbackAccess{revision: 3, member: true, status: state.AccountActive}
			f.h.Access = access
			caller := keyCaller(callbackAccount)
			caller.Issuer = func(state.ManagedRealtimeEndpoint) (agents.CallbackIssuer, *api.Problem) {
				return agents.CallbackIssuer{OrgID: "org-1", AccountID: "admin-1", Revision: 3}, nil
			}
			token, code := f.issueAs(t, caller, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "thread-1"})
			if code != http.StatusCreated {
				t.Fatalf("issue = %d", code)
			}
			if status, _ := f.post(t, token, toolResult("call-1", `{"ok":true}`)); status != http.StatusAccepted {
				t.Fatalf("post while the issuer has access = %d, want 202", status)
			}
			tc.change(access)
			if status, _ := f.post(t, token, toolResult("call-2", `{"ok":true}`)); status != http.StatusNotFound {
				t.Fatalf("post after %s = %d, want 404", tc.name, status)
			}
		})
	}
}
