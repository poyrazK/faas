package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// sumoTarget puts its ingestion token in the URL path and query, as Sumo
// Logic and Loggly HTTP sources do.
const sumoTarget = "https://endpoint4.collection.sumologic.example/receiver/v1/http/ZaVnC4dhaV2SECRET?token=ALSOSECRET"

func logDrainPlanApp(t *testing.T, store state.Store, accountID, slug string, drains int) (state.App, []state.AppLogDrain) {
	t.Helper()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: accountID, Slug: slug, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]state.AppLogDrain, 0, drains)
	for i := range drains {
		target := sumoTarget
		if i > 0 {
			target += "&n=" + string(rune('a'+i))
		}
		d, err := store.CreateAppLogDrain(t.Context(), state.AppLogDrain{AppID: app.ID, AccountID: accountID,
			Kind: state.AppLogDrainKindHTTPJSON, TargetURL: target, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return app, out
}

func logDrainHealth(t *testing.T, e apidserver.TestEnv, slug, id string) api.AppLogDrainHealthResponse {
	t.Helper()
	r := e.Do(t, http.MethodGet, "/v1/apps/"+slug+"/log-drains/"+id+"/health", nil, nil)
	var got api.AppLogDrainHealthResponse
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &got) != nil {
		t.Fatalf("health = %d %s", r.Code, r.Body)
	}
	return got
}

// After a downgrade to Free the owner still lists, reads, disables and
// deletes its drains, which delivery has paused; only configuring a drain
// needs a plan that includes them (logs audit 2026-10-31 D3).
func TestLogDrainsStayManageableWhenThePlanStopsCoveringThem(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	acct := *e.Acct()
	_, drains := logDrainPlanApp(t, store, acct.ID, "downgraded", 1)
	drain := drains[0]
	if err := store.UpsertAppLogDrainHealth(t.Context(), state.AppLogDrainHealth{DrainID: drain.ID,
		Status: api.AppLogDrainHealthStatusHealthy, Active: true, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if got := logDrainHealth(t, e, "downgraded", drain.ID); got.Status != api.AppLogDrainHealthStatusHealthy || !got.Active {
		t.Fatalf("pro health = %+v, want healthy and active", got)
	}
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}

	base := "/v1/apps/downgraded/log-drains"
	r := e.Do(t, http.MethodGet, base, nil, nil)
	var listed []api.AppLogDrainResponse
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].ID != drain.ID || !listed[0].Enabled {
		t.Fatalf("free list = %d %s", r.Code, r.Body)
	}
	for _, path := range []string{base + "/" + drain.ID, base + "/" + drain.ID + "/analytics"} {
		if r := e.Do(t, http.MethodGet, path, nil, nil); r.Code != http.StatusOK {
			t.Fatalf("free GET %s = %d %s", path, r.Code, r.Body)
		}
	}
	if got := logDrainHealth(t, e, "downgraded", drain.ID); got.Status != api.AppLogDrainHealthStatusInactive || got.Active {
		t.Fatalf("free health = %+v, want inactive: the plan no longer covers the drain", got)
	}

	on, off, target := true, false, "https://logs.example/elsewhere"
	for name, c := range map[string]struct {
		method string
		path   string
		body   any
	}{
		"create":         {http.MethodPost, base, api.CreateAppLogDrainRequest{Kind: "http_json", TargetURL: target}},
		"enable":         {http.MethodPatch, base + "/" + drain.ID, api.UpdateAppLogDrainRequest{Enabled: &on}},
		"retarget":       {http.MethodPatch, base + "/" + drain.ID, api.UpdateAppLogDrainRequest{TargetURL: &target}},
		"retarget+off":   {http.MethodPatch, base + "/" + drain.ID, api.UpdateAppLogDrainRequest{TargetURL: &target, Enabled: &off}},
		"new credential": {http.MethodPatch, base + "/" + drain.ID, api.UpdateAppLogDrainRequest{AuthHeader: &target}},
	} {
		r := e.Do(t, c.method, c.path, c.body, nil)
		if r.Code != http.StatusPaymentRequired || !strings.Contains(r.Body.String(), api.CodePlanLogDrainsNotAllowed) {
			t.Fatalf("free %s = %d %s, want 402 %s", name, r.Code, r.Body, api.CodePlanLogDrainsNotAllowed)
		}
	}

	r = e.Do(t, http.MethodPatch, base+"/"+drain.ID, api.UpdateAppLogDrainRequest{Enabled: &off}, nil)
	var disabled api.AppLogDrainResponse
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &disabled) != nil || disabled.Enabled {
		t.Fatalf("free disable = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodDelete, base+"/"+drain.ID, nil, nil); r.Code != http.StatusNoContent {
		t.Fatalf("free delete = %d %s", r.Code, r.Body)
	}
}

// A drain past the plan's allowance reads as inactive however recent the
// gateway's last health snapshot, while the ones the plan covers keep
// theirs.
func TestLogDrainHealthInactiveBeyondThePlanAllowance(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	store := *e.Store()
	perApp := api.MustLimitsFor(api.PlanHobby).LogDrainPerApp
	_, drains := logDrainPlanApp(t, store, (*e.Acct()).ID, "over-allowance", perApp+1)
	for _, d := range drains {
		if err := store.UpsertAppLogDrainHealth(t.Context(), state.AppLogDrainHealth{DrainID: d.ID,
			Status: api.AppLogDrainHealthStatusHealthy, Active: true, UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	for i, d := range drains {
		got := logDrainHealth(t, e, "over-allowance", d.ID)
		covered := i < perApp
		if covered != (got.Status == api.AppLogDrainHealthStatusHealthy && got.Active) {
			t.Fatalf("drain %d of %d: health %+v, covered by the plan = %v", i+1, len(drains), got, covered)
		}
	}
}

// Read-only callers see where a drain sends logs but not the token a
// destination carries in its path or query; whoever may change the drain
// sees all of it (logs audit 2026-10-31 D9).
func TestLogDrainTargetHiddenFromReadOnlyCallers(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	creator := *e.Acct()
	_, personal := logDrainPlanApp(t, store, creator.ID, "personal-drains", 1)
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), creator.ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]string{"Authorization": "Bearer " + plain}

	org := apidserver.SeedSharedOrgWithOwner(t, e, "drain-targets", "Drain targets", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "company-drains"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	company, err := store.AppBySlug(t.Context(), "company-drains")
	if err != nil {
		t.Fatal(err)
	}
	shared, err := store.CreateAppLogDrain(t.Context(), state.AppLogDrain{AppID: company.ID, AccountID: creator.ID,
		Kind: state.AppLogDrainKindHTTPJSON, TargetURL: sumoTarget, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, asDeveloper := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, asViewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)

	const masked = "https://endpoint4.collection.sumologic.example/***"
	for _, c := range []struct {
		name    string
		slug    string
		drainID string
		headers map[string]string
		want    string
	}{
		{"personal app, admin key", "personal-drains", personal[0].ID, nil, sumoTarget},
		{"personal app, apps:read key", "personal-drains", personal[0].ID, readOnly, masked},
		{"company app, creator", "company-drains", shared.ID, nil, sumoTarget},
		{"company app, admin", "company-drains", shared.ID, asAdmin, sumoTarget},
		{"company app, developer", "company-drains", shared.ID, asDeveloper, masked},
		{"company app, viewer", "company-drains", shared.ID, asViewer, masked},
	} {
		base := "/v1/apps/" + c.slug + "/log-drains"
		r := e.Do(t, http.MethodGet, base, nil, c.headers)
		var listed []api.AppLogDrainResponse
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].TargetURL != c.want {
			t.Fatalf("%s: list = %d %s, want target %q", c.name, r.Code, r.Body, c.want)
		}
		r = e.Do(t, http.MethodGet, base+"/"+c.drainID, nil, c.headers)
		var got api.AppLogDrainResponse
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &got) != nil || got.TargetURL != c.want {
			t.Fatalf("%s: get = %d %s, want target %q", c.name, r.Code, r.Body, c.want)
		}
	}
}

// The dashboard lists a downgraded owner's drains as paused instead of
// hiding them behind the upgrade prompt.
func TestDashboardShowsLogDrainsThePlanPaused(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFullFull(t, "free", "paused-drains@example.com")
	acct, err := store.AccountByEmail(t.Context(), "paused-drains@example.com")
	if err != nil {
		t.Fatal(err)
	}
	logDrainPlanApp(t, store, acct.ID, "paused-dashboard", 1)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/paused-dashboard/log-drains", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{"require a paid plan", "paused until then", "paused: not covered by the plan", "inactive", "/receiver/v1/http/ZaVnC4dhaV2SECRET"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard body missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "No runtime log drains are configured") {
		t.Fatalf("dashboard says no drains are configured:\n%s", body)
	}
}
