package main

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGlobalOrganizationTargetHTTPAndSDKTwoCells(t *testing.T) {
	d := globalDirectoryHarness(t)
	org := seedSharedOrgWithOwner(t, d, "directory-team", "Directory Team", api.PlanPro)
	caller := directoryOrgKey(t, d, org, d.acct, api.ScopesAdminOnly)
	dsrv := httptest.NewServer(d.h)
	defer dsrv.Close()
	client := api.NewClient(dsrv.URL, caller.key).SetCompletionCache(nil)
	app, err := client.CreateGlobalApp(t.Context(), api.CreateGlobalAppRequest{Slug: "organization-global"})
	if err != nil {
		t.Fatal(err)
	}
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		e, admin, local, regionalOrg := sharedRegionalGrantHTTPFixture(t)
		e.s.controlPlaneCell.Region = region
		srv := httptest.NewTLSServer(e.h)
		defer srv.Close()
		d.s.globalVerifier.Client = srv.Client()
		cell := registerDirectoryCell(t, d, globaldirectory.Cell{ID: e.s.controlPlaneCell.ID, Region: region, APIBase: srv.URL, Enabled: true})
		req := api.AttachGlobalAppTargetRequest{RegionalAppSlug: local.Slug, RegionalAPIKey: admin.key}
		first, err := client.AttachGlobalAppTarget(t.Context(), app.Slug, region, req)
		if err != nil || first.RegionalOrgID != canonicalGlobalOwnerID(regionalOrg.ID) || first.RegionalAccountID != canonicalGlobalOwnerID(local.AccountID) {
			t.Fatalf("shared attachment: %+v %v", first, err)
		}
		// The creator leaves while another current owner supplies proof.
		if err = e.store.TransferOrgOwnership(t.Context(), regionalOrg.ID, e.acct.ID, admin.acct.ID); err != nil {
			t.Fatal(err)
		}
		if err = e.store.RemoveOrgMember(t.Context(), regionalOrg.ID, e.acct.ID); err != nil {
			t.Fatal(err)
		}
		if retry, err := client.AttachGlobalAppTarget(t.Context(), app.Slug, region, req); err != nil || retry.RegionalAppID != first.RegionalAppID {
			t.Fatalf("creator departure: %+v %v", retry, err)
		}
		resolved, err := client.ResolveGlobalApp(t.Context(), app.Slug, region)
		if err != nil || resolved.RegionalAppID != local.ID || resolved.CellID != cell.ID {
			t.Fatalf("resolution: %+v %v", resolved, err)
		}
		personal := api.NewClient(dsrv.URL, d.key).SetCompletionCache(nil)
		if _, err = personal.AttachGlobalAppTarget(t.Context(), app.Slug, region, req); err == nil {
			t.Fatal("personal caller accessed shared directory app")
		}
	}
	if _, err = client.PutGlobalImageRelease(t.Context(), app.Slug, uuid.NewString(), api.PutGlobalImageReleaseRequest{Image: cellImageRequest().Image}); err == nil {
		t.Fatal("shared runtime admitted before quota and billing")
	}
	if err = client.DetachGlobalAppTarget(t.Context(), app.Slug, "eu-fsn"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ResolveGlobalApp(t.Context(), app.Slug, "eu-fsn"); err == nil {
		t.Fatal("detached target resolved")
	}
}

func TestRegionalOrganizationBindingConsentIsolation(t *testing.T) {
	e, admin, app, org := sharedRegionalGrantHTTPFixture(t)
	path := "/v1/cell/apps/" + app.Slug + "/binding"
	assertProblem(t, e.do(t, http.MethodGet, path, nil, nil), http.StatusNotFound, api.CodeNotFound)
	assertProblem(t, admin.do(t, http.MethodGet, path, nil, map[string]string{"X-Active-Org": "wrong-team"}), http.StatusForbidden, api.CodeOrgRoleForbidden)
	assertProblem(t, admin.do(t, http.MethodGet, path+"?org=wrong-team", nil, nil), http.StatusForbidden, api.CodeOrgRoleForbidden)
	for _, role := range []state.OrgRole{state.OrgRoleDeveloper, state.OrgRoleViewer, state.OrgRoleBilling} {
		if err := e.store.UpdateOrgMemberRole(t.Context(), org.ID, admin.acct.ID, role); err != nil {
			t.Fatal(err)
		}
		assertProblem(t, admin.do(t, http.MethodGet, path, nil, nil), http.StatusForbidden, api.CodeOrgRoleForbidden)
	}
	if err := e.store.UpdateOrgMemberRole(t.Context(), org.ID, admin.acct.ID, state.OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	token, hash, err := api.GenerateRegionalReleaseToken()
	if err != nil {
		t.Fatal(err)
	}
	grant := uuid.NewString()
	req := api.PutRegionalReleaseGrantRequest{GlobalAppID: uuid.NewString(), TokenSHA256: hex.EncodeToString(hash), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	grantPath := "/v1/cells/" + e.s.controlPlaneCell.ID + "/apps/" + app.ID + "/release-grants/" + grant
	if rec := admin.do(t, http.MethodPut, grantPath, req, nil); rec.Code != 200 {
		t.Fatalf("consent: %d %s", rec.Code, rec.Body)
	}
	if rec := grantHTTP(t, e, token, http.MethodGet, path, nil); rec.Code != 200 {
		t.Fatalf("scoped proof: %d %s", rec.Code, rec.Body)
	}
	if err = e.store.RemoveOrgMember(t.Context(), org.ID, admin.acct.ID); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, grantHTTP(t, e, token, http.MethodGet, path, nil), http.StatusUnauthorized, api.CodeUnauthorized)
}

func TestGlobalOrganizationTargetRequiresConsentRole(t *testing.T) {
	e := globalDirectoryHarness(t)
	org := seedSharedOrgWithOwner(t, e, "consent-team", "Consent Team", api.PlanPro)
	owner := directoryOrgKey(t, e, org, e.acct, api.ScopesAdminOnly)
	if r := owner.do(t, http.MethodPost, "/v1/global-apps", api.CreateGlobalAppRequest{Slug: "consent-global"}, nil); r.Code != 201 {
		t.Fatal(r.Body)
	}
	a, err := e.store.CreateAccount(t.Context(), "directory-developer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.AddOrgMember(t.Context(), org.ID, a.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	developer := directoryOrgKey(t, e, org, a, api.ScopesAdminOnly)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		assertProblem(t, developer.do(t, method, "/v1/global-apps/consent-global/targets/eu-fsn", map[string]string{}, nil), http.StatusForbidden, api.CodeOrgRoleForbidden)
	}
}
