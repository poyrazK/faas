package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSharedAppMembersOperateReleases — the release review (2026-10-28)
// found rollback, canary advance and recovery, traffic, promote, release
// gates, the route-health guard, freeze status and alert rollbacks missing
// from the organization route map, so on a company app only its creator
// could reach them: an admin got 403 rolling back during an incident.
// Members who deploy now operate releases, viewers read them, and only
// owners and admins change the gates that stop releases.
func TestSharedAppMembersOperateReleases(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "release-team", "Release Team", api.PlanPro)
	if r := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "rel-api"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app: %d %s", r.Code, r.Body)
	}
	store := *e.Store()
	app, err := store.AppBySlug(t.Context(), "rel-api")
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:release-team"})
	if err != nil {
		t.Fatal(err)
	}
	_, admin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, developer := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	zero, revision := 0, int64(0)
	const (
		read = iota
		deploy
		guard
	)
	calls := []struct {
		method, path string
		body         any
		needs        int
	}{
		{"GET", "/v1/apps/rel-api/freeze-status", nil, read},
		{"GET", "/v1/apps/rel-api/release-gates", nil, read},
		{"GET", "/v1/apps/rel-api/route-health/gate", nil, read},
		{"GET", "/v1/apps/rel-api/alert-rollbacks", nil, read},
		{"POST", "/v1/apps/rel-api/rollback", api.RollbackRequest{}, deploy},
		{"POST", "/v1/apps/rel-api/rollouts/recover", map[string]string{"action": "abort"}, deploy},
		{"POST", "/v1/deployments/" + d.ID + "/canary/advance", api.AdvanceCanaryRequest{ExpectedStep: 0}, deploy},
		{"PATCH", "/v1/deployments/" + d.ID + "/traffic", map[string]int{"traffic_percent": 50}, deploy},
		{"POST", "/v1/deployments/" + d.ID + "/promote", map[string]any{}, deploy},
		{"POST", "/v1/apps/rel-api/release-gates", api.CreateReleaseGateRequest{Name: "no_new_errors", Kind: "new_errors", MaxNewGroups: &zero}, guard},
		{"PUT", "/v1/apps/rel-api/route-health/gate", api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &revision}, guard},
	}
	roleForbidden := func(r interface{ String() string }, code int) bool {
		return code == http.StatusForbidden && strings.Contains(r.String(), api.CodeOrgRoleForbidden)
	}
	for _, member := range []struct {
		name    string
		headers map[string]string
		may     int // the highest permission the role holds
	}{{"admin", admin, guard}, {"developer", developer, deploy}, {"viewer", viewer, read}} {
		for _, c := range calls {
			r := e.Do(t, c.method, c.path, c.body, member.headers)
			if forbidden := roleForbidden(r.Body, r.Code); forbidden != (c.needs > member.may) {
				t.Errorf("%s %s %s = %d %s; want forbidden=%v", member.name, c.method, c.path, r.Code, r.Body, c.needs > member.may)
			}
		}
	}
}
