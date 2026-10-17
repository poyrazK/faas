// ADR-1544: delegated team control stays scoped to live Application Auth authority.
package apidserver_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

type productTeamAuthFixture struct {
	mu                                             sync.Mutex
	org, otherOrg, actor, member, role, memberRole string
	unavailable, forbidden, race                   bool
	writes                                         int
	actions                                        map[string]api.ProductTeamActionReceipt
	onMissing                                      func()
}

func (f *productTeamAuthFixture) snapshot() map[string]any {
	value := map[string]any{"actor_id": f.actor, "actor_role": f.role, "members": []map[string]any{{"user_id": f.actor, "role": f.role, "email": "manager@example.test", "created_at": "2026-10-16T10:00:00Z"}, {"user_id": f.member, "role": f.memberRole, "email": "member@example.test", "created_at": "2026-10-16T10:00:00Z"}}, "invitations": []any{}, "roles": []map[string]any{{"name": "manager", "permissions": []string{"org:read", "members:manage"}}, {"name": "member", "permissions": []string{"org:read"}}, {"name": "operator", "permissions": []string{"org:read", "doc:write"}}, {"name": "owner", "permissions": []string{"*"}}, {"name": "billing", "permissions": []string{"org:read"}}}}
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	value["snapshot"], value["protocol"] = hex.EncodeToString(hash[:]), 1
	return value
}

// The Auth transaction and invitation semantics are tested separately against PostgreSQL.
func (f *productTeamAuthFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer live.access.token" {
		w.WriteHeader(401)
		return
	}
	if r.URL.Path == "/authz/v1/orgs" {
		_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []map[string]string{{"id": f.org, "name": "Customer", "role": f.role}, {"id": f.otherOrg, "name": "Other", "role": f.role}}})
		return
	}
	if r.Method == "GET" && r.URL.Path == "/authz/v1/orgs/"+f.otherOrg+"/team" {
		_ = json.NewEncoder(w).Encode(f.snapshot())
		return
	}
	if f.unavailable || !strings.HasPrefix(r.URL.Path, "/authz/v1/orgs/"+f.org+"/") {
		w.WriteHeader(404)
		return
	}
	if f.forbidden {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, `{"code":"forbidden","detail":"private upstream data"}`)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/authz/v1/orgs/"+f.org)
	if path == "/team" && r.Method == "GET" {
		_ = json.NewEncoder(w).Encode(f.snapshot())
		return
	}
	if r.Method == "GET" && strings.HasPrefix(path, "/team-actions/") {
		found, ok := f.actions[strings.TrimPrefix(path, "/team-actions/")]
		if !ok {
			if f.onMissing != nil {
				callback := f.onMissing
				f.onMissing = nil
				callback()
			}
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(found)
		return
	}
	if path == "/team-actions" && r.Method == "GET" {
		actions := []api.ProductTeamActionReceipt{}
		for _, action := range f.actions {
			actions = append(actions, action)
		}
		_ = json.NewEncoder(w).Encode(api.ProductTeamActionHistory{Actions: actions})
		return
	}
	if path != "/team-actions" || r.Method != "POST" {
		w.WriteHeader(404)
		return
	}
	var body struct {
		api.ProductTeamActionRequest
		Snapshot string          `json:"expected_snapshot"`
		Context  json.RawMessage `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(400)
		return
	}
	if f.race {
		f.race = false
		f.memberRole = "billing"
	}
	if body.Snapshot != f.snapshot()["snapshot"] {
		w.WriteHeader(409)
		_, _ = io.WriteString(w, `{"code":"team_review_changed"}`)
		return
	}
	before := f.memberRole
	result := map[string]any{"user_id": body.TargetID, "before_role": before, "after_role": body.Role, "email": "member@example.test"}
	if body.Action == "set_role" {
		f.memberRole = body.Role
	}
	if body.Action == "invite" {
		result = map[string]any{"id": uuid.NewString(), "email": body.Email, "role": body.Role, "before_role": "", "after_role": body.Role, "expires_at": "2026-10-23T10:00:00Z", "delivered": false}
	}
	request, _ := json.Marshal(body)
	raw, _ := json.Marshal(result)
	receipt := api.ProductTeamActionReceipt{ID: body.ID, OrganizationID: f.org, ActorID: f.actor, ActorRole: f.role, Action: body.Action, Request: request, Result: raw, CreatedAt: time.Now().UTC()}
	f.actions[body.ID] = receipt
	f.writes++
	response := map[string]any{"receipt": receipt, "replayed": false}
	if body.Action == "invite" {
		response["token"] = strings.Repeat("s", 43)
	}
	_ = json.NewEncoder(w).Encode(response)
}

func TestProductControlCustomerTeamHTTP(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "team@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "team-product"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "team-auth"})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}, Access: &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"owner": {api.ProductPermissionViewWork}, "manager": {api.ProductPermissionManageTeam}, "member": {api.ProductPermissionViewWork}, "operator": {api.ProductPermissionRunWork}, "billing": {api.ProductPermissionViewBilling}}}}
	initial, err := store.PutProductCatalog(ctx, acct.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := store.CreatePlatformTenant(ctx, acct.ID, "other", "Other", 100)
	if err != nil {
		t.Fatal(err)
	}
	fake := &productTeamAuthFixture{org: uuid.NewString(), otherOrg: uuid.NewString(), actor: uuid.NewString(), member: uuid.NewString(), role: "manager", memberRole: "member", actions: map[string]api.ProductTeamActionReceipt{}}
	for _, customer := range []api.ProductCustomer{{TenantID: tenant.ID, AuthOrganizationID: fake.org, Plan: "starter"}, {TenantID: other.ID, AuthOrganizationID: fake.otherOrg, Plan: "starter"}} {
		if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, customer); err != nil {
			t.Fatal(err)
		}
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, acct.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(fake)
	defer upstream.Close()
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := apidserver.MintDashboardSession(ctx, store, mgr, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	srv.ProductControl().AuthClient = upstream.Client()
	srv.ProductControl().AuthOrigin = func(_ context.Context, account state.Account, slug string) (state.App, string, error) {
		if account.ID != acct.ID || slug != auth.Slug {
			return state.App{}, "", state.ErrNotFound
		}
		return auth, upstream.URL, nil
	}
	handler := srv.Handler()
	base := "/v1/product-control/customer/" + app.Slug
	admin := "/v1/apps/" + app.Slug + "/product/team-policy"
	const origin = "https://portal.example"
	call := func(method, path string, body any, credential, org, requestOrigin string, status int) *httptest.ResponseRecorder {
		t.Helper()
		raw, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest(method, origin+path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", requestOrigin)
		r.Header.Set("X-Product-Organization", org)
		r.Header.Set("X-Product-Role", "owner")
		switch credential {
		case "customer":
			r.AddCookie(productAccessCookie(auth.ID))
		case "dashboard":
			r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sid})
		default:
			if credential != "" {
				r.Header.Set("Authorization", "Bearer "+credential)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	for _, route := range []struct{ method, path string }{{"GET", admin}, {"POST", admin + "/preview"}, {"PUT", admin}, {"GET", base + "/team"}, {"POST", base + "/team/preview"}, {"POST", base + "/team/confirm"}, {"GET", base + "/team/actions"}, {"GET", base + "/team/actions/" + uuid.NewString()}, {"GET", "/v1/product-control/portal-team.js"}} {
		call(route.method, route.path, nil, "customer", fake.org, origin, 404)
	}
	srv.WithProductControlEnabled(true)
	for _, credential := range []string{"", "customer"} {
		call("GET", admin, nil, credential, fake.org, origin, 401)
	}
	policy := api.ProductTeamPolicy{Delegations: map[string][]string{"manager": {"member", "operator"}}}
	call("POST", admin+"/preview", policy, "dashboard", fake.org, "https://foreign.example", 403)
	call("POST", admin+"/preview", api.ProductTeamPolicy{Delegations: map[string][]string{"manager": {"foreign"}}}, key, fake.org, origin, 400)
	w := call("POST", admin+"/preview", policy, key, fake.org, origin, 200)
	var review api.ProductTeamPolicyPreview
	if err = json.Unmarshal(w.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil || current.Revision != initial.Revision {
		t.Fatal("read-only policy review changed head", err)
	}
	for range 2 {
		call("PUT", admin, review.Change, key, fake.org, origin, 200)
	}
	w = call("GET", base+"/product", nil, "customer", fake.org, origin, 200)
	var portal api.ProductPortal
	if err = json.Unmarshal(w.Body.Bytes(), &portal); err != nil || !portal.Access.Permissions.ManageTeam || portal.Usage != nil || portal.Catalog.Spec.Team != nil || portal.Catalog.Spec.Access != nil || len(portal.Bindings) != 0 {
		t.Fatal("team-only portal projection leaked unrelated data", err)
	}
	call("GET", base+"/billing", nil, "customer", fake.org, origin, 403)
	call("POST", base+"/start", map[string]any{}, "customer", fake.org, origin, 403)
	w = call("GET", base+"/team", nil, "customer", fake.org, origin, 200)
	var team api.ProductTeamView
	if err = json.Unmarshal(w.Body.Bytes(), &team); err != nil || team.Members[0].CanManage || !team.Members[1].CanManage || len(team.AssignableRoles) != 2 {
		t.Fatal("unsafe team controls", err)
	}
	request := api.ProductTeamActionRequest{ID: uuid.NewString(), Action: "set_role", TargetID: fake.member, Role: "operator"}
	call("POST", base+"/team/preview", request, "customer", fake.org, "https://foreign.example", 403)
	call("POST", base+"/team/preview", map[string]any{"id": request.ID, "action": request.Action, "target_id": fake.member, "role": request.Role, "tenant_id": other.ID}, "customer", fake.org, origin, 400)
	badRole := request
	badRole.Role = "owner"
	call("POST", base+"/team/preview", badRole, "customer", fake.org, origin, 403)
	self := request
	self.TargetID = fake.actor
	call("POST", base+"/team/preview", self, "customer", fake.org, origin, 403)
	preview := func(r api.ProductTeamActionRequest) api.ProductTeamActionConfirmation {
		t.Helper()
		response := call("POST", base+"/team/preview", r, "customer", fake.org, origin, 200)
		var value api.ProductTeamActionPreview
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return api.ProductTeamActionConfirmation{Request: value.Request, AuthSnapshot: value.AuthSnapshot, ExpectedHash: value.Hash}
	}
	confirm := preview(request)
	if fake.writes != 0 {
		t.Fatal("preview changed Auth")
	}
	bad := confirm
	bad.ExpectedHash = strings.Repeat("0", 64)
	call("POST", base+"/team/confirm", bad, "customer", fake.org, origin, 409)
	w = call("POST", base+"/team/confirm", confirm, "customer", fake.org, origin, 200)
	if strings.Contains(w.Body.String(), "expected_snapshot") || strings.Contains(w.Body.String(), "context") {
		t.Fatal("receipt leaked upstream context")
	}
	fake.mu.Lock()
	fake.memberRole = "member"
	fake.mu.Unlock()
	w = call("POST", base+"/team/confirm", confirm, "customer", fake.org, origin, 200)
	var result api.ProductTeamActionResult
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || !result.Replayed || fake.writes != 1 || fake.memberRole != "member" {
		t.Fatal("old retry reapplied an earlier role", err)
	}
	changed := confirm
	changed.Request.Role = "member"
	call("POST", base+"/team/confirm", changed, "customer", fake.org, origin, 409)
	call("GET", base+"/team/actions/"+request.ID, nil, "customer", fake.otherOrg, origin, 404)
	call("GET", base+"/team/actions?tenant_id="+other.ID, nil, "customer", fake.org, origin, 400)
	w = call("GET", base+"/team/actions", nil, "customer", fake.org, origin, 200)
	var history api.ProductTeamActionHistory
	if err = json.Unmarshal(w.Body.Bytes(), &history); err != nil || len(history.Actions) != 1 {
		t.Fatal("retry duplicated team history", err)
	}
	// An Auth membership change between review verification and dispatch rejects the write.
	request.ID = uuid.NewString()
	fresh := preview(request)
	fake.mu.Lock()
	fake.race = true
	fake.mu.Unlock()
	call("POST", base+"/team/confirm", fresh, "customer", fake.org, origin, 409)
	fake.mu.Lock()
	fake.memberRole = "member"
	fake.mu.Unlock()
	invite := preview(api.ProductTeamActionRequest{ID: uuid.NewString(), Action: "invite", Email: " FRIEND@Example.Test ", Role: "member"})
	w = call("POST", base+"/team/confirm", invite, "customer", fake.org, origin, 200)
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || !strings.HasPrefix(result.InvitationURL, upstream.URL+"/ui/invitation#token=") || strings.Contains(string(result.Receipt.Result), "token") {
		t.Fatal("manual invitation was not scoped or leaked into history", err)
	}
	w = call("POST", base+"/team/confirm", invite, "customer", fake.org, origin, 200)
	result = api.ProductTeamActionResult{}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.InvitationURL != "" || !result.Replayed {
		t.Fatal("invitation token was returned on recovery", err)
	}
	// Suspension blocks new mutations while retaining read-only receipt recovery.
	customer, err := store.GetProductCustomer(ctx, acct.ID, app.ID, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	customer.Suspended = true
	if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/team/preview", request, "customer", fake.org, origin, 403)
	call("POST", base+"/team/confirm", confirm, "customer", fake.org, origin, 200)
	customer.Suspended = false
	if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.forbidden = true
	fake.mu.Unlock()
	w = call("GET", base+"/team", nil, "customer", fake.org, origin, 403)
	if strings.Contains(w.Body.String(), "private upstream") {
		t.Fatal("Auth error leaked")
	}
	fake.mu.Lock()
	fake.forbidden = false
	fake.unavailable = true
	fake.mu.Unlock()
	call("GET", base+"/team", nil, "customer", fake.org, origin, 503)
	fake.mu.Lock()
	fake.unavailable = false
	fake.role = "owner"
	fake.mu.Unlock()
	call("GET", base+"/team", nil, "customer", fake.org, origin, 403)
	fake.mu.Lock()
	fake.role = "manager"
	fake.mu.Unlock()
	// Product policy is checked again after Auth lookups and before dispatch.
	request.ID = uuid.NewString()
	late := preview(request)
	fake.mu.Lock()
	fake.onMissing = func() {
		latest, e := store.GetProductCatalog(ctx, acct.ID, app.ID, "")
		if e != nil {
			t.Error(e)
			return
		}
		latest.Spec.Team = &api.ProductTeamPolicy{Delegations: map[string][]string{}}
		if _, e = store.PutProductCatalog(ctx, acct.ID, app.ID, latest.Spec); e != nil {
			t.Error(e)
		}
	}
	writes := fake.writes
	fake.mu.Unlock()
	call("POST", base+"/team/confirm", late, "customer", fake.org, origin, 409)
	fake.mu.Lock()
	if fake.writes != writes {
		t.Error("a changed Product policy reached the Auth write")
	}
	fake.mu.Unlock()
	latest, err := store.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	latest.Spec.Team = &policy
	if _, err = store.PutProductCatalog(ctx, acct.ID, app.ID, latest.Spec); err != nil {
		t.Fatal(err)
	}
	// An organization remapping ends the old customer boundary.
	customer.AuthOrganizationID = uuid.NewString()
	if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/team/confirm", confirm, "customer", fake.org, origin, 403)
	customer.AuthOrganizationID = fake.org
	if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	// The owner dashboard requires an Origin, CSRF token and signed exact review.
	console := "/dashboard/apps/" + app.Slug + "/product-control/team-policy"
	jar := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
	browser := func(method, path string, form url.Values, requestOrigin string, status int) *httptest.ResponseRecorder {
		t.Helper()
		data := ""
		if form != nil {
			data = form.Encode()
		}
		r := httptest.NewRequest(method, origin+path, strings.NewReader(data))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", requestOrigin)
		for _, cookie := range jar {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("owner console %s: %d want %d: %s", path, w.Code, status, w.Body.String())
		}
		for _, cookie := range w.Result().Cookies() {
			jar[cookie.Name] = cookie
		}
		return w
	}
	page := browser("GET", console, nil, "", 200)
	form := onboardingForm(t, page.Body.String(), "/team-policy/review")
	form.Set("action", "close")
	form.Del("actor")
	form.Del("role")
	missing := ownerConsoleCopy(form)
	missing.Del("csrf_token")
	browser("POST", console+"/review", missing, origin, 403)
	browser("POST", console+"/review", form, "https://foreign.example", 403)
	page = browser("POST", console+"/review", form, origin, 200)
	approval := onboardingForm(t, page.Body.String(), "/team-policy/confirm")
	tampered := ownerConsoleCopy(approval)
	tampered.Set("payload", approval.Get("payload")+"A")
	browser("POST", console+"/confirm", tampered, origin, 400)
	for range 2 {
		browser("POST", console+"/confirm", approval, origin, 200)
	}
	call("PUT", admin, review.Change, key, fake.org, origin, 409)
	call("POST", base+"/team/preview", request, "customer", fake.org, origin, 403)
	call("POST", base+"/team/confirm", confirm, "customer", fake.org, origin, 200)
	srv.WithProductControlEnabled(false)
	for _, route := range []struct{ method, path string }{{"GET", console}, {"POST", console + "/review"}, {"POST", console + "/confirm"}} {
		browser(route.method, route.path, nil, origin, 404)
	}
}
