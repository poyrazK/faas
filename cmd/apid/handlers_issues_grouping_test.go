//go:build !no_pg

package main

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1111
func TestIssueFailureSiteGroupingThroughProductionRouter(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-grouping")
	dep := issueSeedDeployment(t, e, app, 1)
	token := issueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug + "/issues/"
	event := api.IssueEvent{
		EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error",
		Message: "customer 123 failed", Frames: []api.IssueFrame{
			{File: "/app/src/checkout.ts", Function: "charge", Line: 10, InApp: true},
			{File: "/app/src/handler.ts", Function: "post", Line: 20, InApp: true},
		},
	}
	legacy := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	issueDecode[api.Issue](t, e.do(t, "POST", base+legacy.IssueID+"/actions", api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, nil), 200)
	event.EventID, event.GroupingStrategy, event.ErrorCode = uuid.NewString(), issues.FailureSiteStrategy, "PAYMENT_404"
	first := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	if first.IssueID == legacy.IssueID {
		t.Fatal("opt-in rewrote legacy identity")
	}
	duplicate := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	if !duplicate.Duplicate || duplicate.IssueID != first.IssueID {
		t.Fatal("version 2 retry was not idempotent")
	}
	conflict := event
	conflict.ErrorCode = "PAYMENT_500"
	if w := issueSend(t, e, app.Slug, token, conflict); w.Code != 409 {
		t.Fatalf("changed grouping payload accepted: %d", w.Code)
	}
	event.EventID, event.Message, event.Frames[1].Function = uuid.NewString(), "customer 789 failed", "worker"
	second := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	if second.IssueID != first.IssueID {
		t.Fatal("caller change split failure")
	}
	event.EventID, event.ErrorCode = uuid.NewString(), "PAYMENT_500"
	other := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	if other.IssueID == first.IssueID {
		t.Fatal("different error codes merged")
	}

	// Parsed stack exports and structured SDK frames must find the same group.
	attributes := []any{}
	for key, value := range map[string]string{
		"exception.type": "Error", "exception.message": "different wording",
		"exception.stacktrace":            "Error: different wording\n    at charge (/build/release2/src/checkout.ts:90:5)\n    at anotherCaller (/app/other.ts:50:5)",
		"gregale.issue.grouping_strategy": issues.FailureSiteStrategy,
		"gregale.issue.error_code":        "PAYMENT_404",
	} {
		attributes = append(attributes, map[string]any{"key": key, "value": map[string]string{"stringValue": value}})
	}
	export := map[string]any{"resourceLogs": []any{map[string]any{"scopeLogs": []any{map[string]any{"logRecords": []any{map[string]any{"timeUnixNano": fmt.Sprint(time.Now().UnixNano()), "attributes": attributes}}}}}}}
	for range 2 {
		w := e.do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events/otlp/logs", export, map[string]string{"Authorization": "Bearer " + token.Token})
		if w.Code != http.StatusOK {
			t.Fatalf("OTLP grouping ingest: %d %s", w.Code, w.Body.String())
		}
	}
	detail := issueDecode[api.IssueDetail](t, e.do(t, "GET", base+first.IssueID, nil, nil), 200)
	if detail.Issue.GroupingVersion != 2 || detail.Issue.EventCount != 3 || len(detail.Events) != 3 {
		t.Fatalf("grouping or exporter retry mismatch: %+v", detail)
	}
	for _, occurrence := range detail.Events {
		explanation := occurrence.GroupingExplanation
		if explanation == nil || explanation.Basis != "failure-site" || explanation.DiscriminatorKind != "error-code" || explanation.DiscriminatorValue != "PAYMENT_404" || len(explanation.Frames) != 1 || explanation.Frames[0].File != "src/checkout.ts" {
			t.Fatalf("missing verified occurrence explanation: %+v", explanation)
		}
		if occurrence.GroupingStrategy != issues.FailureSiteStrategy || occurrence.ErrorCode != "PAYMENT_404" {
			t.Fatal("grouping evidence not retained")
		}
	}
	resolved := issueDecode[api.Issue](t, e.do(t, "POST", base+first.IssueID+"/actions", api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, nil), 200)
	event.EventID, event.ErrorCode, event.OccurredAt = uuid.NewString(), "PAYMENT_404", resolved.ResolvedAt.Add(time.Millisecond)
	recurrence := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), 202)
	if recurrence.IssueID != first.IssueID || !recurrence.Regressed {
		t.Fatal("version 2 lost release-aware recurrence")
	}
	old := issueDecode[api.IssueDetail](t, e.do(t, "GET", base+legacy.IssueID, nil, nil), 200)
	if old.Issue.GroupingVersion != 1 || old.Issue.EventCount != 1 || old.Issue.State != "resolved" {
		t.Fatal("opt-in changed legacy history or resolution")
	}
	if len(old.Events) != 1 || old.Events[0].GroupingExplanation == nil || old.Events[0].GroupingExplanation.Basis != "application-frames" || len(old.Events[0].GroupingExplanation.Frames) != 2 {
		t.Fatal("legacy occurrence grouping was not explained")
	}
}

func TestIssueGroupingBatchValidatesBeforeAnyWrites(t *testing.T) {
	now := time.Now().UTC()
	first := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", GroupingStrategy: issues.FailureSiteStrategy}
	second := first
	second.EventID, second.FrameOrder = uuid.NewString(), "unknown"
	inputs, err := prepareIssueOTLP([]api.IssueEvent{first, second}, state.IssueCredential{}, state.Account{Plan: api.PlanHobby}, now)
	if err == nil || inputs != nil {
		t.Fatal("partially validated grouping batch can be committed")
	}
}
