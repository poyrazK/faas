package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// supportFixture is an operator environment plus a customer organization
// "acme" with an owner, an admin, a developer and two apps.
type supportFixture struct {
	env                     apidserver.TestEnv
	store                   *state.MemStore
	org                     state.Org
	owner, admin, developer state.Account
	shop, other             state.App
}

func newSupportFixture(t *testing.T, enabled bool) supportFixture {
	t.Helper()
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	store := *e.Store()
	if enabled {
		(*e.S()).SeedPlatformFlag(platformflags.SupportGrants, platformflags.Definition{Enabled: true, Default: true})
	}
	ctx := t.Context()
	f := supportFixture{env: e, store: store}
	var err error
	for _, p := range []struct {
		acct  *state.Account
		email string
	}{{&f.owner, "owner@acme.test"}, {&f.admin, "admin@acme.test"}, {&f.developer, "dev@acme.test"}} {
		if *p.acct, err = store.CreateAccount(ctx, p.email, api.PlanPro); err != nil {
			t.Fatal(err)
		}
	}
	if f.org, err = store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro}); err != nil {
		t.Fatal(err)
	}
	for acct, role := range map[string]state.OrgRole{f.owner.ID: state.OrgRoleOwner, f.admin.ID: state.OrgRoleAdmin, f.developer.ID: state.OrgRoleDeveloper} {
		if err := store.AddOrgMember(ctx, f.org.ID, acct, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	for slug, app := range map[string]*state.App{"shop": &f.shop, "other": &f.other} {
		if *app, err = store.CreateApp(ctx, state.App{AccountID: f.owner.ID, OrgID: f.org.ID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f supportFixture) grant(t *testing.T, by state.Account, body string) api.SupportGrant {
	t.Helper()
	rec := f.env.DoSession(t, by.ID, http.MethodPost, "/v1/orgs/acme/support-access/grants", json.RawMessage(body), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("grant = %d %s, want 201", rec.Code, rec.Body.String())
	}
	var g api.SupportGrant
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func (f supportFixture) asSupport(t *testing.T, grantID, method, path string, body any) (int, string) {
	t.Helper()
	rec := f.env.DoOperator(t, method, path, body, map[string]string{api.SupportGrantHeader: grantID})
	return rec.Code, rec.Body.String()
}

// A grant admits the operator session to exactly the covered app and the
// reviewed routes; without the header the operator has no access at all.
func TestSupportGrantAdmitsOperatorToCoveredRoutesOnly(t *testing.T) {
	f := newSupportFixture(t, true)
	g := f.grant(t, f.owner, `{"apps":["shop"],"ttl_hours":4,"reason":"deploy is stuck"}`)
	if g.Status != api.SupportGrantActive || g.AllApps || len(g.Apps) != 1 || g.Apps[0] != "shop" {
		t.Fatalf("grant view = %+v", g)
	}
	if code, body := f.asSupport(t, g.ID, http.MethodGet, "/v1/apps/shop", nil); code != http.StatusOK {
		t.Fatalf("support read of a covered app = %d %s, want 200", code, body)
	}
	if rec := f.env.DoOperator(t, http.MethodGet, "/v1/apps/shop", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("operator without a grant = %d %s, want 404", rec.Code, rec.Body.String())
	}
	if code, body := f.asSupport(t, g.ID, http.MethodGet, "/v1/apps/other", nil); code != http.StatusNotFound {
		t.Fatalf("support read of an uncovered app = %d %s, want 404", code, body)
	}
	if code, body := f.asSupport(t, g.ID, http.MethodDelete, "/v1/apps/shop", nil); code != http.StatusForbidden || !strings.Contains(body, "Not covered") {
		t.Fatalf("support delete = %d %s, want 403 not covered", code, body)
	}
	if code, body := f.asSupport(t, g.ID, http.MethodGet, "/v1/apps/shop/secrets", nil); code != http.StatusForbidden {
		t.Fatalf("support secrets read = %d %s, want 403", code, body)
	}
	if code, body := f.asSupport(t, g.ID, http.MethodPost, "/v1/apps/shop/rollback", map[string]any{"restore_databases": true}); code != http.StatusForbidden || !strings.Contains(body, "restore_databases") {
		t.Fatalf("support rollback restoring databases = %d %s, want 403", code, body)
	}
	if code, body := f.asSupport(t, g.ID, http.MethodPost, "/v1/apps/shop/deployments/source-ref", map[string]any{"repo": "acme/shop", "ref": "main"}); code != http.StatusForbidden || !strings.Contains(body, "No connected repository") {
		t.Fatalf("support deploy of an unconnected app = %d %s, want 403", code, body)
	}
	if err := f.store.UpsertGithubInstallBinding(t.Context(), state.GitHubBinding{AppID: f.shop.ID, AccountID: f.owner.ID, BindingID: uuid.NewString(), InstallID: 7, RepoFullName: "acme/shop", ProductionBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	// Only the connected repository at its production branch: a SHA or a
	// pull-request ref could be a fork commit support wrote itself.
	for _, tc := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"repo": "evil/repo", "ref": "main"}, "connected repository"},
		{map[string]any{"repo": "acme/shop", "ref": "refs/pull/1/head"}, "production branch"},
		{map[string]any{"repo": "acme/shop", "ref": "4b825dc642cb6eb9a060e54bf8d69288fbee4904"}, "production branch"},
		{map[string]any{"repo": "acme/shop", "ref": "main", "rollback_on_5xx": false}, "rollback_on_5xx"},
		{map[string]any{"repo": "acme/shop", "ref": "main", "environment": "staging"}, "environment"},
	} {
		if code, body := f.asSupport(t, g.ID, http.MethodPost, "/v1/apps/shop/deployments/source-ref", tc.body); code != http.StatusForbidden || !strings.Contains(body, tc.want) {
			t.Fatalf("support deploy %v = %d %s, want 403 %q", tc.body, code, body, tc.want)
		}
	}
	if code, body := f.asSupport(t, g.ID, http.MethodPost, "/v1/apps/shop/deployments/source-ref", map[string]any{"repo": "acme/shop", "ref": "refs/heads/main"}); code == http.StatusForbidden || code == http.StatusNotFound && !strings.Contains(body, "github_install_not_found") {
		// The fixture has no GitHub App installation, so the deploy handler
		// itself answers github_install_not_found: the gate admitted it.
		t.Fatalf("support deploy of the production branch = %d %s, want it admitted", code, body)
	}
	if code, body := f.asSupport(t, g.ID, http.MethodPost, "/v1/apps/shop/rollback", map[string]any{"alert_rule_id": "x"}); code != http.StatusForbidden || !strings.Contains(body, "alert_rule_id") {
		t.Fatalf("support rollback with alert attribution = %d %s, want 403", code, body)
	}
	if code, body := f.asSupport(t, g.ID, http.MethodPost, "/v1/apps/shop/restart", map[string]any{"replicas": 3}); code != http.StatusForbidden {
		t.Fatalf("support restart with a body = %d %s, want 403", code, body)
	}
	code, body := f.asSupport(t, g.ID, http.MethodPost, "/v1/apps/shop/restart", nil)
	if code == http.StatusForbidden || code == http.StatusNotFound || code == http.StatusUnauthorized {
		t.Fatalf("support restart = %d %s, want it admitted", code, body)
	}
}

// Revoking ends access immediately, and only owners and admins may grant.
func TestSupportGrantRevokeAndRoles(t *testing.T) {
	f := newSupportFixture(t, true)
	if rec := f.env.DoSession(t, f.developer.ID, http.MethodPost, "/v1/orgs/acme/support-access/grants", json.RawMessage(`{}`), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("developer grant = %d %s, want 403", rec.Code, rec.Body.String())
	}
	g := f.grant(t, f.admin, `{}`)
	if !g.AllApps || g.CreatedBy != f.admin.Email {
		t.Fatalf("org-wide grant view = %+v", g)
	}
	if code, body := f.asSupport(t, g.ID, http.MethodGet, "/v1/apps/other", nil); code != http.StatusOK {
		t.Fatalf("support read under an org-wide grant = %d %s, want 200", code, body)
	}
	if rec := f.env.DoSession(t, f.owner.ID, http.MethodPost, "/v1/orgs/acme/support-access/grants/"+g.ID+"/revoke", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s, want 204", rec.Code, rec.Body.String())
	}
	if code, body := f.asSupport(t, g.ID, http.MethodGet, "/v1/apps/shop", nil); code != http.StatusNotFound || !strings.Contains(body, "Support grant not found") {
		t.Fatalf("support after revoke = %d %s, want 404", code, body)
	}
	var overview api.SupportAccess
	rec := f.env.DoSession(t, f.developer.ID, http.MethodGet, "/v1/orgs/acme/support-access", nil, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &overview) != nil || len(overview.Grants) != 1 || overview.Grants[0].Status != api.SupportGrantRevoked {
		t.Fatalf("member overview = %d %s, want the revoked grant", rec.Code, rec.Body.String())
	}
}

// A grant ends when the admin who gave it is no longer an admin.
func TestSupportGrantEndsWhenGranterLosesRole(t *testing.T) {
	f := newSupportFixture(t, true)
	g := f.grant(t, f.admin, `{"apps":["shop"]}`)
	if err := f.store.UpdateOrgMemberRole(t.Context(), f.org.ID, f.admin.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	if code, body := f.asSupport(t, g.ID, http.MethodGet, "/v1/apps/shop", nil); code != http.StatusNotFound {
		t.Fatalf("support after the granter's demotion = %d %s, want 404", code, body)
	}
}

// Only an allowlisted operator's MFA session uses a grant: not a customer,
// and not a key.
func TestSupportGrantNeedsOperatorSession(t *testing.T) {
	f := newSupportFixture(t, true)
	g := f.grant(t, f.owner, `{"apps":["shop"]}`)
	rec := f.env.DoSession(t, f.developer.ID, http.MethodGet, "/v1/apps/other", nil, map[string]string{api.SupportGrantHeader: g.ID})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("customer session with a grant header = %d %s, want 403", rec.Code, rec.Body.String())
	}
	rec = f.env.Do(t, http.MethodGet, "/v1/apps/shop", nil, map[string]string{api.SupportGrantHeader: g.ID})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Operator session required") {
		t.Fatalf("operator key with a grant header = %d %s, want 403", rec.Code, rec.Body.String())
	}
}

// Granting needs a signed-in browser, the flag, and a customer: an
// operator account never grants.
func TestSupportGrantCreationGuards(t *testing.T) {
	f := newSupportFixture(t, true)
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := f.store.CreateOrgAPIKey(t.Context(), f.org.ID, f.owner.ID, hash, "ci", api.ScopesAdminOnly, nil); err != nil {
		t.Fatal(err)
	}
	rec := flowBearer((*f.env.H()), http.MethodPost, "/v1/orgs/acme/support-access/grants", pt, `{}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeBrowserSessionRequired) {
		t.Fatalf("grant with a key = %d %s, want 403 %s", rec.Code, rec.Body.String(), api.CodeBrowserSessionRequired)
	}
	if err := f.store.AddOrgMember(t.Context(), f.org.ID, (*f.env.Acct()).ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if rec := f.env.DoSession(t, "", http.MethodPost, "/v1/orgs/acme/support-access/grants", json.RawMessage(`{}`), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("operator granting = %d %s, want 403", rec.Code, rec.Body.String())
	}

	off := newSupportFixture(t, false)
	if rec := off.env.DoSession(t, off.owner.ID, http.MethodPost, "/v1/orgs/acme/support-access/grants", json.RawMessage(`{}`), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("grant with the flag off = %d %s, want 403", rec.Code, rec.Body.String())
	}
}

// Support asks; the owners and admins are mailed; an owner approves and the
// grant carries the request.
func TestSupportAccessRequestApproval(t *testing.T) {
	f := newSupportFixture(t, true)
	rec := f.env.DoOperator(t, http.MethodPost, "/v1/admin/support-access/requests", api.OperatorSupportAccessRequest{Org: "acme", Reason: "your deploys fail at build"}, map[string]string{"Idempotency-Key": "support-request-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("request = %d %s, want 201", rec.Code, rec.Body.String())
	}
	var req api.SupportAccessRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatal(err)
	}
	var overview api.SupportAccess
	rec = f.env.DoSession(t, f.owner.ID, http.MethodGet, "/v1/orgs/acme/support-access", nil, nil)
	if json.Unmarshal(rec.Body.Bytes(), &overview) != nil || len(overview.Requests) != 1 || overview.Requests[0].Status != state.SupportRequestOpen {
		t.Fatalf("overview = %s, want the open request", rec.Body.String())
	}
	rec = f.env.DoSession(t, f.owner.ID, http.MethodPost, "/v1/orgs/acme/support-access/requests/"+req.ID+"/approve", json.RawMessage(`{"apps":["shop"],"ttl_hours":168}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("approve = %d %s, want 201", rec.Code, rec.Body.String())
	}
	var g api.SupportGrant
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil || g.RequestID != req.ID || g.Reason != "your deploys fail at build" {
		t.Fatalf("approved grant = %+v %v", g, err)
	}
	if rec := f.env.DoSession(t, f.owner.ID, http.MethodPost, "/v1/orgs/acme/support-access/requests/"+req.ID+"/approve", json.RawMessage(`{}`), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second approval = %d %s, want 404", rec.Code, rec.Body.String())
	}
	rec = f.env.DoOperator(t, http.MethodGet, "/v1/admin/support-access/grants", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), g.ID) {
		t.Fatalf("operator grant list = %d %s, want the grant", rec.Code, rec.Body.String())
	}
	if rec := f.env.DoSession(t, f.owner.ID, http.MethodPost, "/v1/orgs/acme/support-access/grants", json.RawMessage(`{"ttl_hours":169}`), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("grant over 7 days = %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// The dashboard page lists the organizations an owner manages, with their
// grants; a developer manages none.
func TestSupportAccessDashboardPage(t *testing.T) {
	f := newSupportFixture(t, true)
	g := f.grant(t, f.owner, `{"apps":["shop"],"reason":"stuck deploy"}`)
	rec := f.env.DoSession(t, f.owner.ID, http.MethodGet, "/dashboard/support-access", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page = %d %s, want 200", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Support access", "acme", "stuck deploy", "/dashboard/support-access/acme/grants/" + g.ID + "/revoke"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// A link cannot put its own words in a first-party banner.
	rec = f.env.DoSession(t, f.owner.ID, http.MethodGet, "/dashboard/support-access?error=Email+your+codes+to+evil&done=Hacked", nil, nil)
	if strings.Contains(rec.Body.String(), "evil") || strings.Contains(rec.Body.String(), "Hacked") {
		t.Fatalf("page echoes query text: %s", rec.Body.String())
	}
	rec = f.env.DoSession(t, f.owner.ID, http.MethodGet, "/dashboard/support-access?done=revoked", nil, nil)
	if !strings.Contains(rec.Body.String(), "Support access revoked.") {
		t.Fatal("page lacks the fixed revoked message")
	}
	rec = f.env.DoSession(t, f.developer.ID, http.MethodGet, "/dashboard/support-access", nil, nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "stuck deploy") {
		t.Fatalf("developer page = %d, want 200 without the grant", rec.Code)
	}
}
