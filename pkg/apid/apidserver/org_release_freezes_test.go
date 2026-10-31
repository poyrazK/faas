package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// orgFreezeDo sends a request as a member and decodes a 2xx answer into out.
func orgFreezeDo(t *testing.T, e apidserver.TestEnv, headers map[string]string, method, path string, body, out any) (int, string) {
	t.Helper()
	rec := e.Do(t, method, path, body, headers)
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s %s response %s: %v", method, path, rec.Body, err)
		}
	}
	return rec.Code, rec.Body.String()
}

// newCompanyFreezeApp seeds a company organization whose owner created the
// app slug.
func newCompanyFreezeApp(t *testing.T, slug string) (apidserver.TestEnv, state.Org) {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, slug+"-co", "Freeze Co", api.PlanPro)
	if r := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: slug}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app: %d %s", r.Code, r.Body)
	}
	return e, org
}

// adr: 20261019071733 amendment 4 — a company organization owns its apps'
// freezes. Members other than the app's creator freeze and lift them; a
// developer may create and lift their own windows, and standing policies
// and everyone else's freezes need an owner or admin.
func TestCompanyFreezesFollowMemberRoles(t *testing.T) {
	e, org := newCompanyFreezeApp(t, "frz-co")
	store := *e.Store()
	_, admin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, developer := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	window := api.CreateReleaseFreezeRequest{Kind: api.ReleaseFreezeKindWindow, Reason: "incident 4711"}
	weekly := api.CreateReleaseFreezeRequest{Kind: api.ReleaseFreezeKindWeekly, Reason: "no weekend releases",
		Weekly: &api.ReleaseFreezeWeekly{Days: []string{"sat", "sun"}, Start: "00:00", DurationMinutes: 1440, TimeZone: "UTC"}}

	var devWindow api.ReleaseFreeze
	if code, body := orgFreezeDo(t, e, developer, "POST", "/v1/apps/frz-co/freezes", window, &devWindow); code != http.StatusCreated {
		t.Fatalf("developer freezes a company app = %d %s", code, body)
	}
	if devWindow.Org != org.Slug || devWindow.App != "frz-co" || !devWindow.Active {
		t.Fatalf("developer's freeze = %+v, want one owned by %s", devWindow, org.Slug)
	}
	if code, body := orgFreezeDo(t, e, developer, "POST", "/v1/apps/frz-co/freezes", weekly, nil); code != http.StatusForbidden || !strings.Contains(body, api.CodeOrgRoleForbidden) {
		t.Fatalf("developer creates a weekly policy = %d %s, want 403", code, body)
	}
	if code, body := orgFreezeDo(t, e, viewer, "POST", "/v1/apps/frz-co/freezes", window, nil); code != http.StatusForbidden {
		t.Fatalf("viewer freezes = %d %s, want 403", code, body)
	}
	var adminWeekly api.ReleaseFreeze
	if code, body := orgFreezeDo(t, e, admin, "POST", "/v1/apps/frz-co/freezes", weekly, &adminWeekly); code != http.StatusCreated {
		t.Fatalf("admin creates a weekly policy = %d %s", code, body)
	}
	var companyWide api.ReleaseFreeze
	if code, body := orgFreezeDo(t, e, developer, "POST", "/v1/orgs/"+org.Slug+"/freezes", window, &companyWide); code != http.StatusCreated {
		t.Fatalf("developer freezes the company = %d %s", code, body)
	}
	if companyWide.App != "" || companyWide.Org != org.Slug {
		t.Fatalf("company-wide freeze = %+v", companyWide)
	}
	if code, body := orgFreezeDo(t, e, developer, "POST", "/v1/orgs/"+org.Slug+"/freezes", api.CreateReleaseFreezeRequest{App: "frz-co", Kind: "window", Reason: "x"}, nil); code != http.StatusBadRequest {
		t.Fatalf("company route naming an app = %d %s, want 400", code, body)
	}

	var list api.ListReleaseFreezesResponse
	if code, body := orgFreezeDo(t, e, viewer, "GET", "/v1/apps/frz-co/freezes", nil, &list); code != http.StatusOK || len(list.Freezes) != 3 {
		t.Fatalf("viewer lists the app's freezes = %d %s, want 3", code, body)
	}
	if code, body := orgFreezeDo(t, e, viewer, "GET", "/v1/orgs/"+org.Slug+"/freezes", nil, &list); code != http.StatusOK || len(list.Freezes) != 3 {
		t.Fatalf("viewer lists the company's freezes = %d %s, want 3", code, body)
	}

	if code, body := orgFreezeDo(t, e, developer, "DELETE", "/v1/apps/frz-co/freezes/"+adminWeekly.ID, nil, nil); code != http.StatusForbidden {
		t.Fatalf("developer lifts the admin's policy = %d %s, want 403", code, body)
	}
	if code, body := orgFreezeDo(t, e, developer, "DELETE", "/v1/apps/frz-co/freezes/"+companyWide.ID, nil, nil); code != http.StatusNotFound {
		t.Fatalf("app route lifts a company-wide freeze = %d %s, want 404", code, body)
	}
	if code, body := orgFreezeDo(t, e, developer, "DELETE", "/v1/apps/frz-co/freezes/"+devWindow.ID, nil, nil); code != http.StatusOK {
		t.Fatalf("developer lifts their own window = %d %s", code, body)
	}
	if code, body := orgFreezeDo(t, e, admin, "DELETE", "/v1/orgs/"+org.Slug+"/freezes/"+companyWide.ID, nil, nil); code != http.StatusOK {
		t.Fatalf("admin lifts the developer's company-wide freeze = %d %s", code, body)
	}
	if code, body := orgFreezeDo(t, e, admin, "DELETE", "/v1/apps/frz-co/freezes/"+adminWeekly.ID, nil, nil); code != http.StatusOK {
		t.Fatalf("admin lifts their policy = %d %s", code, body)
	}

	var activity api.ListOrgActivityResponse
	if code, body := orgFreezeDo(t, e, admin, "GET", "/v1/orgs/"+org.Slug+"/activity", nil, &activity); code != http.StatusOK {
		t.Fatalf("activity = %d %s", code, body)
	}
	kinds := map[string]int{}
	for _, item := range activity.Items {
		kinds[item.Kind]++
	}
	if kinds["release.freeze_created"] != 3 || kinds["release.freeze_lifted"] != 3 {
		t.Fatalf("activity kinds = %v, want 3 freezes created and 3 lifted", kinds)
	}
}

// A company-wide freeze refuses releases of an app another member created,
// and the creator's own account freezes no longer reach company apps.
func TestCompanyFreezeRefusesReleasesAndAccountFreezesStayPersonal(t *testing.T) {
	e, org := newCompanyFreezeApp(t, "frz-cw")
	apidserver.MustSeedDeployment(t, e, "frz-mine")
	_, developer := sharedAppMember(t, *e.Store(), org, state.OrgRoleDeveloper, api.ScopesAdminOnly)

	var personal api.ReleaseFreeze
	if rec := guardDo(t, e, "POST", "/v1/freezes", api.CreateReleaseFreezeRequest{Kind: "window", Reason: "my apps only"}, &personal); rec.Code != http.StatusCreated {
		t.Fatalf("account-wide freeze = %d %s", rec.Code, rec.Body)
	}
	var status api.ReleaseFreezeStatus
	if rec := guardDo(t, e, "GET", "/v1/apps/frz-mine/freeze-status", nil, &status); rec.Code != http.StatusOK || !status.Frozen {
		t.Fatalf("personal app status = %d %+v, want frozen", rec.Code, status)
	}
	if rec := guardDo(t, e, "GET", "/v1/apps/frz-cw/freeze-status", nil, &status); rec.Code != http.StatusOK || status.Frozen {
		t.Fatalf("company app status under an account freeze = %d %+v, want not frozen", rec.Code, status)
	}
	if rec := guardDo(t, e, "POST", "/v1/apps/frz-cw/deployments", api.CreateDeploymentRequest{Image: imageRef('a')}, nil); decodeProblemCode(rec.Body.Bytes()) == api.CodeReleaseFrozen {
		t.Fatalf("an account freeze refused a company app's release: %s", rec.Body)
	}

	rec := guardDo(t, e, "POST", "/v1/freezes", api.CreateReleaseFreezeRequest{App: "frz-cw", Kind: "window", Reason: "old CLI"}, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "/v1/orgs/"+org.Slug+"/freezes") {
		t.Fatalf("account route naming a company app = %d %s, want 409 naming the company route", rec.Code, rec.Body)
	}

	var companyWide api.ReleaseFreeze
	if code, body := orgFreezeDo(t, e, developer, "POST", "/v1/orgs/"+org.Slug+"/freezes", api.CreateReleaseFreezeRequest{Kind: "window", Reason: "company holiday"}, &companyWide); code != http.StatusCreated {
		t.Fatalf("company-wide freeze = %d %s", code, body)
	}
	rec = guardDo(t, e, "POST", "/v1/apps/frz-cw/deployments", api.CreateDeploymentRequest{Image: imageRef('b')}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("creator's release under a company freeze = %d %s, want 409", rec.Code, rec.Body)
	}
	if p := decodeProblem(t, rec.Body.Bytes()); p.Code != api.CodeReleaseFrozen || len(p.Freezes) != 1 || p.Freezes[0].ID != companyWide.ID || p.Freezes[0].Org != org.Slug {
		t.Fatalf("problem = %+v", p)
	}
	if rec := guardDo(t, e, "GET", "/v1/apps/frz-mine/freeze-status", nil, &status); rec.Code != http.StatusOK || len(status.Active) != 1 || status.Active[0].ID != personal.ID {
		t.Fatalf("personal app status under a company freeze = %d %+v, want only the account freeze", rec.Code, status)
	}

	var list api.ListReleaseFreezesResponse
	if rec := guardDo(t, e, "GET", "/v1/freezes", nil, &list); rec.Code != http.StatusOK || len(list.Freezes) != 1 || list.Freezes[0].ID != personal.ID {
		t.Fatalf("account freezes = %d %+v, want only the personal freeze", rec.Code, list)
	}
	if rec := guardDo(t, e, "DELETE", "/v1/freezes/"+companyWide.ID, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("account route lifts a company freeze = %d %s, want 404", rec.Code, rec.Body)
	}
}

// An after-rollback policy on a company app freezes it under the company,
// so every member sees the freeze and an admin can lift it.
func TestCompanyAfterRollbackPolicyFreezesUnderTheOrganization(t *testing.T) {
	e, org := newCompanyFreezeApp(t, "frz-auto-co")
	store := *e.Store()
	_, admin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	policy := api.CreateReleaseFreezeRequest{Kind: "after_rollback", AfterRollbackMinutes: 60, Reason: "settle after automatic rollbacks"}
	if code, body := orgFreezeDo(t, e, admin, "POST", "/v1/apps/frz-auto-co/freezes", policy, nil); code != http.StatusCreated {
		t.Fatalf("policy = %d %s", code, body)
	}
	app, err := store.AppBySlug(t.Context(), "frz-auto-co")
	if err != nil {
		t.Fatal(err)
	}
	rolledBack := "6f1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	e.FreezeAfterAutomaticRollback(t.Context(), app, rolledBack, "the post-release watch rolled back v2")

	var status api.ReleaseFreezeStatus
	if rec := guardDo(t, e, "GET", "/v1/apps/frz-auto-co/freeze-status", nil, &status); rec.Code != http.StatusOK || len(status.Active) != 1 {
		t.Fatalf("status after the rollback = %d %+v", rec.Code, status)
	}
	auto := status.Active[0]
	if auto.Source != "automatic_rollback" || auto.Org != org.Slug || auto.App != "frz-auto-co" || auto.SourceDeploymentID != rolledBack {
		t.Fatalf("automatic freeze = %+v, want one owned by %s", auto, org.Slug)
	}
	var activity api.ListOrgActivityResponse
	if code, body := orgFreezeDo(t, e, admin, "GET", "/v1/orgs/"+org.Slug+"/activity", nil, &activity); code != http.StatusOK {
		t.Fatalf("activity = %d %s", code, body)
	}
	found := false
	for _, item := range activity.Items {
		found = found || item.Kind == "release.freeze_created" && item.Actor.Type == string(state.OrgActivityActorSystem) && item.Resource.ID == auto.ID
	}
	if !found {
		t.Fatalf("activity has no system freeze for %s: %+v", auto.ID, activity.Items)
	}
	if code, body := orgFreezeDo(t, e, admin, "DELETE", "/v1/apps/frz-auto-co/freezes/"+auto.ID, nil, nil); code != http.StatusOK {
		t.Fatalf("admin lifts the automatic freeze = %d %s", code, body)
	}
}

// A member who leaves the organization loses its freezes.
func TestRemovedMemberCannotReachCompanyFreezes(t *testing.T) {
	e, org := newCompanyFreezeApp(t, "frz-gone")
	store := *e.Store()
	member, developer := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	var frozen api.ReleaseFreeze
	if code, body := orgFreezeDo(t, e, developer, "POST", "/v1/apps/frz-gone/freezes", api.CreateReleaseFreezeRequest{Kind: "window", Reason: "deploying a fix"}, &frozen); code != http.StatusCreated {
		t.Fatalf("freeze = %d %s", code, body)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/apps/frz-gone/freezes"},
		{"DELETE", "/v1/apps/frz-gone/freezes/" + frozen.ID},
		{"GET", "/v1/orgs/" + org.Slug + "/freezes"},
		{"DELETE", "/v1/orgs/" + org.Slug + "/freezes/" + frozen.ID},
	} {
		if code, body := orgFreezeDo(t, e, developer, c.method, c.path, nil, nil); code < 400 {
			t.Errorf("removed member %s %s = %d %s, want refused", c.method, c.path, code, body)
		}
	}
	var status api.ReleaseFreezeStatus
	if rec := guardDo(t, e, "GET", "/v1/apps/frz-gone/freeze-status", nil, &status); rec.Code != http.StatusOK || !status.Frozen {
		t.Fatalf("company freeze after its creator left = %d %+v, want still frozen", rec.Code, status)
	}
}

// A personal organization's apps keep their account's freezes, so the
// company route refuses to create freezes that would cover nothing.
func TestPersonalOrganizationUsesAccountFreezes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ownerID := e.Acct().ID
	personal, err := store.CreateOrg(t.Context(), state.Org{Slug: state.PersonalOrgSlug(ownerID), Name: "Personal", Personal: true,
		PersonalOwnerAccountID: &ownerID, Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), personal.ID, ownerID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	rec := guardDo(t, e, "POST", "/v1/orgs/"+personal.Slug+"/freezes", api.CreateReleaseFreezeRequest{Kind: "window", Reason: "x"}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("personal organization freeze = %d %s, want 409", rec.Code, rec.Body)
	}
}

// memberKey mints another org key for member, with scopes, as a CI key is.
func memberKey(t *testing.T, store state.Store, org state.Org, member state.Account, scopes []string) map[string]string {
	t.Helper()
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOrgAPIKey(t.Context(), org.ID, member.ID, hash, "ci", scopes, nil); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + plain}
}

// adr: 20261020085706 amendment 5 — a key that can only deploy, as CI holds,
// cannot switch off the guards it deploys past: lifting a freeze, deleting a
// gate and changing or removing the approval policy need the admin scope,
// even on an admin's key (release review 2026-11-01).
func TestDeployKeysCannotSwitchOffReleaseGuards(t *testing.T) {
	e, org := newCompanyFreezeApp(t, "guard-ci")
	store := *e.Store()
	admin, adminCLI := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	ci := memberKey(t, store, org, admin, api.ScopesDeployWriteSurface)
	var frz api.ReleaseFreeze
	if code, body := orgFreezeDo(t, e, adminCLI, "POST", "/v1/apps/guard-ci/freezes",
		api.CreateReleaseFreezeRequest{Kind: api.ReleaseFreezeKindWindow, Reason: "release train"}, &frz); code != http.StatusCreated {
		t.Fatalf("admin freezes = %d %s", code, body)
	}
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"DELETE", "/v1/apps/guard-ci/freezes/" + frz.ID, nil},
		{"DELETE", "/v1/orgs/" + org.Slug + "/freezes/" + frz.ID, nil},
		{"DELETE", "/v1/apps/guard-ci/release-gates/checkout", nil},
		{"PUT", "/v1/apps/guard-ci/release-approval-policy", api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1}},
		{"DELETE", "/v1/apps/guard-ci/release-approval-policy", nil},
	} {
		if code, body := orgFreezeDo(t, e, ci, req.method, req.path, req.body, nil); code != http.StatusForbidden || !strings.Contains(body, api.CodeForbidden) {
			t.Fatalf("deploy key %s %s = %d %s, want 403 %s", req.method, req.path, code, body, api.CodeForbidden)
		}
	}
	if code, body := orgFreezeDo(t, e, adminCLI, "DELETE", "/v1/apps/guard-ci/freezes/"+frz.ID, nil, nil); code != http.StatusOK {
		t.Fatalf("admin lifts with an admin key = %d %s", code, body)
	}
}

// adr: 20261019071733 amendment 5 — overriding a company's freeze needs the
// right to lift it: a developer cannot ship past an admin's freeze, nor can
// a key that can only deploy, while the admin signed in can (release review
// 2026-11-01).
func TestCompanyFreezeOverrideNeedsTheRightToLiftIt(t *testing.T) {
	e, org := newCompanyFreezeApp(t, "frz-ovr")
	store := *e.Store()
	admin, adminCLI := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, developer := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	ci := memberKey(t, store, org, admin, api.ScopesDeployWriteSurface)
	if code, body := orgFreezeDo(t, e, adminCLI, "POST", "/v1/orgs/"+org.Slug+"/freezes",
		api.CreateReleaseFreezeRequest{Kind: api.ReleaseFreezeKindWindow, Reason: "release train"}, nil); code != http.StatusCreated {
		t.Fatalf("admin freezes the company = %d %s", code, body)
	}
	deploy := api.CreateDeploymentRequest{Image: imageRef('c'), OverrideFreeze: "hotfix 4711"}
	for name, headers := range map[string]map[string]string{"developer": developer, "admin's deploy key": ci} {
		code, body := orgFreezeDo(t, e, headers, "POST", "/v1/apps/frz-ovr/deployments", deploy, nil)
		if code != http.StatusForbidden || !strings.Contains(body, api.CodeReleaseFreezeOverrideNotAllowed) {
			t.Fatalf("%s overrides the company freeze = %d %s, want 403 %s", name, code, body, api.CodeReleaseFreezeOverrideNotAllowed)
		}
	}
	code, body := orgFreezeDo(t, e, adminCLI, "POST", "/v1/apps/frz-ovr/deployments", deploy, nil)
	if p := decodeProblemCode([]byte(body)); p == api.CodeReleaseFreezeOverrideNotAllowed || p == api.CodeReleaseFrozen {
		t.Fatalf("admin overrides the company freeze = %d %s, want it accepted", code, body)
	}
}
