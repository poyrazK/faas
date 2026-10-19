//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 1459
func TestIssueReleaseComparisonAndBulkRoutes(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "release-triage")
	baseline := apidserver.IssueSeedDeployment(t, e, app, 1)
	candidate := apidserver.IssueSeedDeployment(t, e, app, 2)
	token := apidserver.IssueCreateToken(t, e, app.Slug, candidate)
	headers := map[string]string{"Authorization": "Bearer " + token.Token}
	ids := []string{}
	for _, kind := range []string{"PaymentError", "ExportError"} {
		w := e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: kind, Message: "failed"}, headers)
		accepted := apidserver.IssueDecode[api.IssueEventResponse](t, w, 202)
		ids = append(ids, accepted.IssueID)
	}
	path := "/v1/apps/" + app.Slug + "/issue-release-comparison?baseline=" + baseline.ID + "&candidate=" + candidate.ID
	w := e.Do(t, "GET", path, nil, nil)
	comparison := apidserver.IssueDecode[api.IssueReleaseComparison](t, w, 200)
	if comparison.Candidate.ObservedIssueEvents != 2 || comparison.Candidate.NewIssueGroups != 2 || comparison.Candidate.HTTP5xxRate != nil || comparison.HTTP5xxRateDelta != nil || !strings.Contains(w.Body.String(), `"http_5xx_rate":null`) {
		t.Fatalf("comparison: %+v", comparison)
	}
	if w = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issue-release-comparison?baseline="+baseline.ID+"&candidate="+baseline.ID, nil, nil); w.Code != 400 {
		t.Fatalf("same deployment: %d", w.Code)
	}
	foreign := apidserver.SeedPGApp(t, e, "foreign-triage")
	foreignDep := apidserver.IssueSeedDeployment(t, e, foreign, 1)
	if w = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issue-release-comparison?baseline="+baseline.ID+"&candidate="+foreignDep.ID, nil, nil); w.Code != 404 {
		t.Fatalf("foreign deployment: %d", w.Code)
	}
	bulkPath := "/v1/apps/" + app.Slug + "/issues/bulk-actions"
	batch := api.BulkIssueActionRequest{IssueIDs: ids, Action: api.IssueActionRequest{Action: "resolve", FixedDeploymentID: candidate.ID}}
	if w = e.Do(t, "POST", bulkPath, batch, headers); w.Code != 401 && w.Code != 403 {
		t.Fatalf("reporter gained triage authority: %d", w.Code)
	}
	retryHeaders := map[string]string{"Idempotency-Key": uuid.NewString()}
	first := e.Do(t, "POST", bulkPath, batch, retryHeaders)
	out := apidserver.IssueDecode[api.BulkIssueActionResponse](t, first, 200)
	if len(out.Items) != 2 || out.Items[0].State != "resolved" || out.Items[1].State != "resolved" {
		t.Fatalf("batch: %+v", out)
	}
	if retry := e.Do(t, "POST", bulkPath, batch, retryHeaders); retry.Code != 200 || retry.Body.String() != first.Body.String() {
		t.Fatalf("retry changed result: %d %s", retry.Code, retry.Body.String())
	}
	batch.Action = api.IssueActionRequest{Action: "reopen"}
	batch.IssueIDs = []string{ids[0], uuid.NewString()}
	if w = e.Do(t, "POST", bulkPath, batch, nil); w.Code != 404 {
		t.Fatalf("missing selection: %d", w.Code)
	}
	w = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues/"+ids[0], nil, nil)
	var detail api.IssueDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 || detail.Issue.State != "resolved" {
		t.Fatal("partial bulk action persisted")
	}
}
