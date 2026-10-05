package main

// adr: 610
import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func sharedRegionalGrantHTTPFixture(t *testing.T) (testEnv, testEnv, state.App, state.Org) {
	t.Helper()
	e, app := cellDeploymentHarness(t)
	org := seedSharedOrgWithOwner(t, e, "regional-team", "Regional Team", api.PlanPro)
	app.ID, app.Slug, app.OrgID = uuid.NewString(), "regional-team", org.ID
	app, err := e.store.CreateApp(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := e.store.CreateAccount(t.Context(), "regional-admin@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkAccountEmailVerified(t.Context(), actor.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.store.AddOrgMember(t.Context(), org.ID, actor.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	return e, directoryOrgKey(t, e, org, actor, api.ScopesAdminOnly), app, org
}

func TestRegionalOrganizationGrantHTTPAndSDK(t *testing.T) {
	e, admin, app, org := sharedRegionalGrantHTTPFixture(t)
	regional := httptest.NewServer(e.h)
	defer regional.Close()
	client := api.NewClient(regional.URL, admin.key).SetCompletionCache(nil)
	token, hash, err := api.GenerateRegionalReleaseToken()
	if err != nil {
		t.Fatal(err)
	}
	id, global := uuid.NewString(), uuid.NewString()
	req := api.PutRegionalReleaseGrantRequest{GlobalAppID: global, TokenSHA256: hex.EncodeToString(hash), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	first, err := client.PutRegionalReleaseGrant(t.Context(), e.s.controlPlaneCell.ID, app.ID, id, req)
	if err != nil || first.Status != "active" {
		t.Fatalf("shared SDK registration: %+v %v", first, err)
	}
	if retry, err := client.PutRegionalReleaseGrant(t.Context(), e.s.controlPlaneCell.ID, app.ID, id, req); err != nil || !retry.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("shared SDK replay: %+v %v", retry, err)
	}
	var grants state.RegionalReleaseGrantStore = e.store
	g, err := grants.GetRegionalReleaseGrant(t.Context(), e.acct.ID, e.s.controlPlaneCell.ID, app.ID, id)
	if err != nil || g.AccountID != app.AccountID || g.IssuerAccountID != admin.acct.ID || g.OrgID != org.ID {
		t.Fatalf("consent identity: %+v %v", g, err)
	}
	delegated := api.NewClient(regional.URL, token).SetCompletionCache(nil)
	if got, err := delegated.GetRegionalReleaseGrant(t.Context(), e.s.controlPlaneCell.ID, app.ID, id); err != nil || got.GlobalAppID != global {
		t.Fatalf("scoped metadata: %+v %v", got, err)
	}
	for _, path := range []string{"/v1/account", "/v1/apps", "/v1/apps/" + app.Slug} {
		got := grantHTTP(t, e, token, http.MethodGet, path, nil)
		assertProblem(t, got, http.StatusForbidden, api.CodeForbidden)
	}
	grantPath := "/v1/cells/" + e.s.controlPlaneCell.ID + "/apps/" + app.ID + "/release-grants/" + id
	assertProblem(t, grantHTTP(t, e, token, http.MethodPut, grantPath, req), http.StatusForbidden, api.CodeForbidden)
	assertProblem(t, admin.do(t, http.MethodGet, grantPath, nil, map[string]string{"X-Active-Org": "another-team"}), http.StatusForbidden, api.CodeOrgRoleForbidden)
	assertProblem(t, admin.do(t, http.MethodGet, grantPath+"?org=another-team", nil, nil), http.StatusForbidden, api.CodeOrgRoleForbidden)
	assertProblem(t, e.do(t, http.MethodGet, grantPath, nil, nil), http.StatusNotFound, api.CodeNotFound)
	// Shared consent does not bypass the pending runtime quota/billing gate.
	releaseID := uuid.NewString()
	got := grantHTTP(t, e, token, http.MethodPut, cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, releaseID), api.PutCellImageReleaseRequest{Image: cellImageRequest().Image, Ordering: &api.CellImageReleaseOrdering{GlobalAppID: global, Generation: 1}})
	if got.Code < 400 {
		t.Fatalf("shared runtime admitted without quota protocol: %d %s", got.Code, got.Body)
	}
	if _, err = e.store.RegionalImageReleaseReceipt(t.Context(), e.s.controlPlaneCell.ID, app.ID, releaseID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("shared runtime left receipt", err)
	}
	if err = e.store.UpdateOrgMemberRole(t.Context(), org.ID, admin.acct.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, grantHTTP(t, e, token, http.MethodGet, grantPath, nil), http.StatusUnauthorized, api.CodeUnauthorized)
	assertProblem(t, admin.do(t, http.MethodPut, grantPath, req, nil), http.StatusForbidden, api.CodeOrgRoleForbidden)
	owner := directoryOrgKey(t, e, org, e.acct, api.ScopesAdminOnly)
	if got := owner.do(t, http.MethodDelete, grantPath, nil, nil); got.Code != http.StatusOK {
		t.Fatalf("replacement administrator revoke: %d %s", got.Code, got.Body)
	}
}

func TestRegionalOrganizationGrantRoleAndScopeIsolation(t *testing.T) {
	for _, role := range []state.OrgRole{state.OrgRoleDeveloper, state.OrgRoleViewer, state.OrgRoleBilling} {
		t.Run(string(role), func(t *testing.T) {
			e, admin, app, org := sharedRegionalGrantHTTPFixture(t)
			if err := e.store.UpdateOrgMemberRole(t.Context(), org.ID, admin.acct.ID, role); err != nil {
				t.Fatal(err)
			}
			path := "/v1/cells/" + e.s.controlPlaneCell.ID + "/apps/" + app.ID + "/release-grants/" + uuid.NewString()
			assertProblem(t, admin.do(t, http.MethodPut, path, api.PutRegionalReleaseGrantRequest{}, nil), http.StatusForbidden, api.CodeOrgRoleForbidden)
		})
	}
	e, admin, app, org := sharedRegionalGrantHTTPFixture(t)
	path := "/v1/cells/" + e.s.controlPlaneCell.ID + "/apps/" + app.ID + "/release-grants/" + uuid.NewString()
	readonly := directoryOrgKey(t, e, org, admin.acct, []string{api.ScopeAppsRead})
	assertProblem(t, readonly.do(t, http.MethodPut, path, api.PutRegionalReleaseGrantRequest{}, nil), http.StatusForbidden, api.CodeForbidden)
	if err := e.store.UpdateOrgStatus(t.Context(), org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, admin.do(t, http.MethodPut, path, api.PutRegionalReleaseGrantRequest{}, nil), http.StatusForbidden, api.CodeOrgRoleForbidden)
}
