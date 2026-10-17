//go:build !no_pg

package apidserver_test

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"testing"
	"time"
)

// adr: 1463
func TestIssueTrackerLinkManagementAndReporterIsolation(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "tracker-links")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	reporter := map[string]string{"Authorization": "Bearer " + token.Token}
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "PaymentError", Message: "failed"}, reporter), 202)
	path := base + "/issues/" + accepted.IssueID + "/tracker-links"
	in := api.PutIssueTrackerLinkRequest{URL: "https://github.com/ACME/Repo/issues/0042#comment", Title: "Fix payment"}
	for _, method := range []string{"GET", "POST"} {
		w := e.Do(t, method, path, in, reporter)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("reporter gained tracker-link access", w.Code)
		}
	}
	linked := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", path, in, nil), 200)
	if linked.Provider != "github" || linked.URL != "https://github.com/acme/repo/issues/42" {
		t.Fatal("URL not canonicalized")
	}
	same := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", path, in, nil), 200)
	if same.ID != linked.ID {
		t.Fatal("POST retry duplicated reference")
	}
	listed := apidserver.IssueDecode[api.ListIssueTrackerLinksResponse](t, e.Do(t, "GET", path, nil, nil), 200)
	if len(listed.Items) != 1 {
		t.Fatal("link not listed")
	}
	if w := e.Do(t, "POST", path, api.PutIssueTrackerLinkRequest{URL: "javascript:alert(1)"}, nil); w.Code != 400 {
		t.Fatal("unsafe URL accepted", w.Code)
	}
	foreign := apidserver.SeedPGApp(t, e, "tracker-foreign")
	foreignPath := "/v1/apps/" + foreign.Slug + "/issues/" + accepted.IssueID + "/tracker-links"
	if w := e.Do(t, "GET", foreignPath, nil, nil); w.Code != 404 {
		t.Fatal("cross-app issue reference exposed", w.Code)
	}
	if w := e.Do(t, "DELETE", path+"/"+linked.ID, nil, reporter); w.Code != 401 && w.Code != 403 {
		t.Fatal("reporter unlinked ticket", w.Code)
	}
	if w := e.Do(t, "DELETE", path+"/"+linked.ID, nil, nil); w.Code != 204 {
		t.Fatal("unlink failed", w.Code, w.Body.String())
	}
	detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base+"/issues/"+accepted.IssueID, nil, nil), 200)
	seen := map[string]bool{}
	for _, activity := range detail.Activity {
		seen[activity.Action] = true
	}
	if !seen["tracker_linked"] || !seen["tracker_unlinked"] {
		t.Fatal("missing tracker audit activity")
	}
}
