// adr: 20261101225122
package agentconnections_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agentconnections"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPolicyCapsSignInScopes(t *testing.T) {
	policy := state.OrgAgentPolicy{AgentsAllowed: true, AllowedScopes: []string{api.ScopeDeployWrite}}
	requested := []string{api.ScopeAppsRead, api.ScopeDeployWrite, api.ScopeSecretsRequest, api.ScopeManagedPostgresManage, api.ScopeManagedPostgresRead}
	if got := agentconnections.CapScopes(policy, requested); !slices.Equal(got, []string{api.ScopeAppsRead, api.ScopeDeployWrite}) {
		t.Fatalf("CapScopes = %v", got)
	}
	if !agentconnections.ExceedsPolicy(policy, requested) || agentconnections.ExceedsPolicy(policy, []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}) {
		t.Fatal("ExceedsPolicy must flag only scopes outside the ceiling")
	}
	if !agentconnections.ExceedsPolicy(state.OrgAgentPolicy{}, []string{api.ScopeAppsRead}) {
		t.Fatal("a policy that doesn't allow agents refuses every key")
	}
	if agentconnections.ValidPolicyScopes([]string{api.ScopeDeployWrite, api.ScopeDeployWrite}) || agentconnections.ValidPolicyScopes([]string{api.ScopeAdmin}) || !agentconnections.ValidPolicyScopes([]string{}) {
		t.Fatal("ValidPolicyScopes must accept only grantable scopes, once each")
	}
}

type httpResult struct {
	code int
	body string
}

func newRequest(method, path, body string) (*http.Request, *httptest.ResponseRecorder) {
	return httptest.NewRequest(method, path, strings.NewReader(body)), httptest.NewRecorder()
}

type orgFixture struct {
	*fixture
	org     state.Org
	deps    agentconnections.OrgDeps
	revoked map[string]string
}

// newOrgFixture is a shared organization owned by the fixture's account,
// with a viewer member.
func newOrgFixture(t *testing.T) (*orgFixture, state.Account) {
	t.Helper()
	f := newFixture(t)
	ctx := context.Background()
	org, err := f.store.CreateOrg(ctx, state.Org{Slug: "agents-co", Name: "Agents Co", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := f.store.CreateAccount(ctx, "viewer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for acct, role := range map[string]state.OrgRole{f.acct.ID: state.OrgRoleOwner, viewer.ID: state.OrgRoleViewer} {
		if err := f.store.AddOrgMember(ctx, org.ID, acct, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	o := &orgFixture{fixture: f, org: org, revoked: map[string]string{}}
	o.deps = agentconnections.OrgDeps{
		Deps:       f.deps,
		AuthzAudit: func() authz.AuditEmitter { return nil },
		RevokeOrg: func(_ *http.Request, _ state.Account, orgID, id, reason string) error {
			o.revoked[id] = reason
			_, err := f.store.RevokeOrgAPIKey(context.Background(), orgID, id)
			return err
		},
		RecordPolicy: func(*http.Request, state.Account, state.OrgAgentPolicy, int) {},
	}
	return o, viewer
}

func (o *orgFixture) orgKey(acct state.Account, label string, scopes []string) state.APIKey {
	o.t.Helper()
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		o.t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	k, err := o.store.CreateOrgAPIKeyWithProvenance(context.Background(), o.org.ID, acct.ID, hash, label, scopes, &expires, "", "", nil)
	if err != nil {
		o.t.Fatal(err)
	}
	return k
}

// call runs h as acct, a member of the organization with role.
func (o *orgFixture) callAs(h func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, role state.OrgRole, method, path, body string) *httpResult {
	req, w := newRequest(method, path, body)
	mem := &state.OrgMembership{OrgID: o.org.ID, AccountID: acct.ID, Role: role}
	req = req.WithContext(authmw.WithPrincipal(req.Context(), acct, nil, mem))
	if i := strings.LastIndex(path, "/"); i >= 0 {
		req.SetPathValue("id", path[i+1:])
	}
	h(w, req, acct)
	return &httpResult{code: w.Code, body: w.Body.String()}
}

// An organization's owners see and revoke every member's agents there;
// a viewer sees none of them.
func TestOrgOwnersSeeAndRevokeTheOrganizationsAgents(t *testing.T) {
	o, viewer := newOrgFixture(t)
	hosted := o.orgKey(viewer, api.MCPLoginKeyLabel, []string{api.ScopeAppsRead})
	agent := o.orgKey(o.acct, "ci-agent", []string{api.ScopeAgent, api.ScopeAppsRead})
	o.orgKey(o.acct, "deploy", []string{api.ScopeAdmin})

	if r := o.callAs(agentconnections.OrgList(o.deps), viewer, state.OrgRoleViewer, http.MethodGet, "/v1/orgs/agents-co/agent-connections", ""); r.code != http.StatusForbidden {
		t.Fatalf("viewer list = %d %s, want 403", r.code, r.body)
	}
	r := o.callAs(agentconnections.OrgList(o.deps), o.acct, state.OrgRoleOwner, http.MethodGet, "/v1/orgs/agents-co/agent-connections", "")
	var list api.AgentConnectionsResponse
	if r.code != http.StatusOK || json.Unmarshal([]byte(r.body), &list) != nil || len(list.Items) != 2 {
		t.Fatalf("owner list = %d %s", r.code, r.body)
	}
	for _, item := range list.Items {
		if item.ID == hosted.ID && (item.Member != "viewer@example.com" || item.OrgID != o.org.ID) {
			t.Fatalf("viewer's hosted connection = %+v", item)
		}
	}
	if r := o.callAs(agentconnections.OrgRevoke(o.deps), o.acct, state.OrgRoleOwner, http.MethodDelete, "/v1/orgs/agents-co/agent-connections/"+hosted.ID, ""); r.code != http.StatusNoContent || o.revoked[hosted.ID] != "agent_disconnected" {
		t.Fatalf("owner revoke = %d %s, revoked %v", r.code, r.body, o.revoked)
	}
	r = o.callAs(agentconnections.OrgRevokeAll(o.deps), o.acct, state.OrgRoleOwner, http.MethodPost, "/v1/orgs/agents-co/agent-connections/revoke_all", "")
	if r.code != http.StatusOK || !strings.Contains(r.body, `"revoked":1`) || o.revoked[agent.ID] == "" {
		t.Fatalf("revoke all = %d %s, revoked %v", r.code, r.body, o.revoked)
	}
}

// Tightening the policy revokes the connections it no longer allows; the
// default allows every capability and every member reads it.
func TestTighteningThePolicyRevokesWhatItNoLongerAllows(t *testing.T) {
	o, viewer := newOrgFixture(t)
	reader := o.orgKey(viewer, api.MCPLoginKeyLabel, []string{api.ScopeAppsRead})
	deployer := o.orgKey(o.acct, api.MCPLoginKeyLabel, []string{api.ScopeAppsRead, api.ScopeDeployWrite, api.ScopeSecretsRequest})

	r := o.callAs(agentconnections.GetPolicy(o.deps), viewer, state.OrgRoleViewer, http.MethodGet, "/v1/orgs/agents-co/agent-policy", "")
	if r.code != http.StatusOK || !strings.Contains(r.body, `"agents_allowed":true`) || !strings.Contains(r.body, api.ScopeManagedPostgresManage) {
		t.Fatalf("default policy = %d %s", r.code, r.body)
	}
	if r := o.callAs(agentconnections.PutPolicy(o.deps), viewer, state.OrgRoleViewer, http.MethodPut, "/v1/orgs/agents-co/agent-policy", `{"agents_allowed":false,"allowed_scopes":[]}`); r.code != http.StatusForbidden {
		t.Fatalf("viewer set = %d, want 403", r.code)
	}
	for _, body := range []string{`{"agents_allowed":true}`, `{"agents_allowed":true,"allowed_scopes":["admin"]}`} {
		if r := o.callAs(agentconnections.PutPolicy(o.deps), o.acct, state.OrgRoleOwner, http.MethodPut, "/v1/orgs/agents-co/agent-policy", body); r.code != http.StatusBadRequest {
			t.Fatalf("set %s = %d, want 400", body, r.code)
		}
	}
	r = o.callAs(agentconnections.PutPolicy(o.deps), o.acct, state.OrgRoleOwner, http.MethodPut, "/v1/orgs/agents-co/agent-policy", `{"agents_allowed":true,"allowed_scopes":["deploy:write"]}`)
	if r.code != http.StatusOK || !strings.Contains(r.body, `"revoked":1`) || o.revoked[deployer.ID] != "agent_policy" || o.revoked[reader.ID] != "" {
		t.Fatalf("narrowing = %d %s, revoked %v", r.code, r.body, o.revoked)
	}
	r = o.callAs(agentconnections.PutPolicy(o.deps), o.acct, state.OrgRoleOwner, http.MethodPut, "/v1/orgs/agents-co/agent-policy", `{"agents_allowed":false,"allowed_scopes":[]}`)
	if r.code != http.StatusOK || o.revoked[reader.ID] != "agent_policy" {
		t.Fatalf("denying agents = %d %s, revoked %v", r.code, r.body, o.revoked)
	}
	if _, err := agentconnections.PolicyFor(context.Background(), o.store, o.org.ID, viewer.ID); !errors.Is(err, agentconnections.ErrAgentsNotAllowed) {
		t.Fatalf("PolicyFor after denying = %v", err)
	}
}

// Only members whose role can see apps may connect an agent, and never to
// a personal organization.
func TestPolicyForChecksTheMembersRole(t *testing.T) {
	o, viewer := newOrgFixture(t)
	ctx := context.Background()
	if policy, err := agentconnections.PolicyFor(ctx, o.store, o.org.ID, viewer.ID); err != nil || !policy.AgentsAllowed {
		t.Fatalf("viewer PolicyFor = %+v, %v", policy, err)
	}
	stranger, err := o.store.CreateAccount(ctx, "stranger@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentconnections.PolicyFor(ctx, o.store, o.org.ID, stranger.ID); !errors.Is(err, agentconnections.ErrNotAgentMember) {
		t.Fatalf("stranger PolicyFor = %v, want ErrNotAgentMember", err)
	}
	if err := o.store.AddOrgMember(ctx, o.org.ID, stranger.ID, state.OrgRoleBilling, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := agentconnections.PolicyFor(ctx, o.store, o.org.ID, stranger.ID); !errors.Is(err, agentconnections.ErrNotAgentMember) {
		t.Fatalf("billing member PolicyFor = %v, want ErrNotAgentMember", err)
	}
}
