package apidserver_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// memCallbackRevocations keeps each thread's latest callback revocation.
type memCallbackRevocations struct {
	mu sync.Mutex
	at map[string]time.Time
}

func (m *memCallbackRevocations) RevokeCallbacks(_ context.Context, _, endpointID, threadID string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.at[endpointID+"/"+threadID] = now
	return now, nil
}

func (m *memCallbackRevocations) CallbacksRevokedAt(_ context.Context, endpointID, threadID string) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, ok := m.at[endpointID+"/"+threadID]
	return at, ok, nil
}

// adr: 20261030125501 §3 — an API key issues and revokes callback URLs only
// for the realtime endpoints whose app the organization check lets it act
// on, as the app's other credentials: the routes name an endpoint, not an
// app, and a company app's endpoints stay under its creator's account. A
// creator demoted below admin, a key bound to another organization and a
// creator removed from the organization reach nothing in the company app's
// agent inbox, and the creator's own app is unaffected.
func TestAgentCallbacksFollowCompanyAppAccess(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, creator, s := *e.Store(), *e.Acct(), *e.S()
	s.ConfigureAgentsForTest()
	s.SetAgentCallbackRevocationsForTest(&memCallbackRevocations{at: map[string]time.Time{}})
	ctx := t.Context()
	owner, err := store.CreateAccount(ctx, "callbacks-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme-agents", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := store.CreateOrg(ctx, state.Org{Slug: "elsewhere-agents", Name: "Elsewhere", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		org, account string
		role         state.OrgRole
	}{{org.ID, owner.ID, state.OrgRoleOwner}, {org.ID, creator.ID, state.OrgRoleAdmin}, {elsewhere.ID, creator.ID, state.OrgRoleOwner}} {
		if err := store.AddOrgMember(ctx, m.org, m.account, m.role, nil); err != nil {
			t.Fatal(err)
		}
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOrgAPIKey(ctx, elsewhere.ID, creator.ID, hash, "elsewhere", api.ScopesAdminOnly, nil); err != nil {
		t.Fatal(err)
	}
	elsewhereKey := map[string]string{"Authorization": "Bearer " + plain}

	endpointFor := func(orgID, slug string) string {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: orgID, Slug: slug, Type: state.AppTypeApp,
			Runtime: "node22", RAMMB: 128, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		endpoint, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
			ID: uuid.NewString(), AccountID: creator.ID, AppID: app.ID, Enabled: true}, 10, 10)
		if err != nil {
			t.Fatal(err)
		}
		return endpoint.ID
	}
	company, own := endpointFor(org.ID, "acme-agent"), endpointFor("", "own-agent")
	// both issues a callback URL for the endpoint and revokes its thread,
	// and returns the two statuses.
	both := func(endpointID string, hdrs map[string]string) [2]int {
		t.Helper()
		issue := e.Do(t, http.MethodPost, "/v1/agent-callbacks",
			api.CreateAgentCallbackRequest{EndpointID: endpointID, ThreadID: "thread-1"}, hdrs)
		revoke := e.Do(t, http.MethodPost, "/v1/agent-callbacks/revocations",
			api.RevokeAgentCallbacksRequest{EndpointID: endpointID, ThreadID: "thread-1"}, hdrs)
		return [2]int{issue.Code, revoke.Code}
	}
	allowed := [2]int{http.StatusCreated, http.StatusOK}
	check := func(stage, endpointID string, hdrs map[string]string, want [2]int) {
		t.Helper()
		if got := both(endpointID, hdrs); got != want {
			t.Errorf("%s: issue, revoke = %v, want %v", stage, got, want)
		}
	}

	check("admin creator, company app", company, nil, allowed)
	check("admin creator, own app", own, nil, allowed)
	check("key bound to another organization, company app", company, elsewhereKey,
		[2]int{http.StatusNotFound, http.StatusNotFound})

	if err := store.UpdateOrgMemberRole(ctx, org.ID, creator.ID, state.OrgRoleDeveloper); err != nil {
		t.Fatal(err)
	}
	check("developer creator, company app", company, nil, [2]int{http.StatusForbidden, http.StatusForbidden})
	check("developer creator, own app", own, nil, allowed)

	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	check("removed creator, company app", company, nil, [2]int{http.StatusNotFound, http.StatusNotFound})
	check("removed creator, own app", own, nil, allowed)
}
