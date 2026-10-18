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

// adr: 1456
func TestIssueDeploymentArtifactSymbolicationRESTAndOTLP(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "artifact-symbolication")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	path := "/v1/apps/" + app.Slug + "/deployments/" + dep.ID + "/issue-source-maps"
	request := api.PublishIssueSourceMapsRequest{Directory: "/app/dist", Manifest: api.IssueSourceMapManifest{Version: 1, Artifacts: []api.IssueSourceMapArtifact{{GeneratedFile: "bundle.js", SHA256: strings.Repeat("a", 64), Payload: `{"version":3,"sourceRoot":"../","sources":["src/checkout.ts"],"names":["charge"],"mappings":"AASAA","sourcesContent":["PRIVATE_SOURCE"]}`}}}}
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "payment failed", GroupingStrategy: "failure-site-v1", SymbolicationStrategy: "deployment-source-maps-v1", StackTrace: "Error: payment failed\n    at a (/app/dist/bundle.js:1:1)"}
	event.Tags = map[string]string{"component": "payments", "email": "private@example.com"}
	event.Breadcrumbs = []api.IssueBreadcrumb{{OccurredAt: event.OccurredAt.Add(-time.Second), Category: "workflow", Level: "info", Message: "starting charge"}}
	headers := map[string]string{"Authorization": "Bearer " + token.Token}
	w := e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", event, headers)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "issue_source_maps_unavailable") {
		t.Fatalf("missing map capture accepted: %d %s", w.Code, w.Body.String())
	}
	w = e.Do(t, "PUT", path, request, nil)
	if w.Code != 200 {
		t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}
	var meta api.IssueSourceMapsMetadata
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil || !meta.Ready {
		t.Fatalf("metadata: %+v %v", meta, err)
	}
	if w = e.Do(t, "PUT", path, request, nil); w.Code != 200 {
		t.Fatal("idempotent publication failed")
	}
	if w = e.Do(t, "PUT", path, request, headers); w.Code != 401 && w.Code != 403 {
		t.Fatal("reporting credential published owner artifacts")
	}
	w = e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", event, headers)
	if w.Code != 202 {
		t.Fatalf("capture: %d %s", w.Code, w.Body.String())
	}
	var accepted api.IssueEventResponse
	_ = json.Unmarshal(w.Body.Bytes(), &accepted)
	w = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues/"+accepted.IssueID, nil, nil)
	var detail api.IssueDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || len(detail.Events) != 1 {
		t.Fatalf("detail: %d %s", w.Code, w.Body.String())
	}
	first := detail.Events[0]
	if first.Frames[0].File != "/app/src/checkout.ts" || first.Frames[0].Function != "charge" || first.Symbolication == nil || first.Symbolication.MappedFrames != 1 || first.GroupingExplanation == nil {
		t.Fatalf("unmapped evidence: %+v", first)
	}
	if first.StackTrace != event.StackTrace || strings.Contains(w.Body.String(), "PRIVATE_SOURCE") {
		t.Fatal("raw evidence changed or source content escaped")
	}
	if len(first.Breadcrumbs) != 1 || len(first.Tags) != 1 || first.Tags["component"] != "payments" {
		t.Fatalf("context persistence: %+v", first)
	}
	for _, filter := range []struct {
		tag   string
		count int
	}{{"component%3Dpayments", 1}, {"component%3Dexports", 0}} {
		w = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues?tag="+filter.tag, nil, nil)
		var list api.ListIssuesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 || len(list.Items) != filter.count {
			t.Fatalf("tag search: %d %s", w.Code, w.Body.String())
		}
	}
	attrs := []map[string]any{}
	for k, v := range map[string]string{"exception.type": "Error", "exception.message": "payment failed", "exception.stacktrace": event.StackTrace, "gregale.issue.grouping_strategy": "failure-site-v1", "gregale.issue.symbolication_strategy": "deployment-source-maps-v1"} {
		attrs = append(attrs, map[string]any{"key": k, "value": map[string]string{"stringValue": v}})
	}
	batch := map[string]any{"resourceLogs": []any{map[string]any{"scopeLogs": []any{map[string]any{"logRecords": []any{map[string]any{"timeUnixNano": event.OccurredAt.UnixNano(), "attributes": attrs}}}}}}}
	w = e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events/otlp/logs", batch, headers)
	if w.Code != 200 {
		t.Fatalf("OTLP: %d %s", w.Code, w.Body.String())
	}
	w = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/issues/"+accepted.IssueID, nil, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if len(detail.Events) != 2 || detail.Issue.EventCount != 2 {
		t.Fatal("REST and OTLP failures split or did not persist")
	}
	if w = e.Do(t, "DELETE", path, nil, nil); w.Code != 200 {
		t.Fatal("retirement failed")
	}
	w = e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", event, headers)
	if w.Code != 202 {
		t.Fatalf("exact retry after retirement: %d %s", w.Code, w.Body.String())
	}
	var duplicate api.IssueEventResponse
	_ = json.Unmarshal(w.Body.Bytes(), &duplicate)
	if !duplicate.Duplicate || duplicate.IssueID != accepted.IssueID {
		t.Fatal("retry changed provenance")
	}
	event.EventID = uuid.NewString()
	if w = e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", event, headers); w.Code != 409 {
		t.Fatal("retired artifacts accepted new mapping")
	}
	changed := request
	changed.Manifest.Artifacts = append([]api.IssueSourceMapArtifact(nil), request.Manifest.Artifacts...)
	changed.Manifest.Artifacts[0].SHA256 = strings.Repeat("b", 64)
	if w = e.Do(t, "PUT", path, changed, nil); w.Code != 409 {
		t.Fatal("retired manifest was overwritten")
	}
	if w = e.Do(t, "PUT", path, request, nil); w.Code != 200 {
		t.Fatal("identical manifest did not restore")
	}
	foreign := apidserver.SeedPGApp(t, e, "foreign-artifacts")
	foreignDep := apidserver.IssueSeedDeployment(t, e, foreign, 1)
	if w = e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/deployments/"+foreignDep.ID+"/issue-source-maps", request, nil); w.Code != 404 {
		t.Fatal("published artifacts across apps")
	}
}
