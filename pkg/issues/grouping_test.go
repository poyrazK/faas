package issues

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestIssueGroupingAcrossReleases(t *testing.T) {
	now := time.Now().UTC()
	in := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "DateFormatError", Message: "invalid date for customer 123", Frames: []api.IssueFrame{{File: "/build/v42/src/export.ts", Function: "generateExport", Line: 42, InApp: true}}}
	_, first, _, err := Normalize(in, now, api.PlanHobby.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	in.EventID = uuid.NewString()
	in.Message = "invalid date for customer 789"
	in.Frames[0].File = "/build/v43/src/export.ts"
	in.Frames[0].Line = 80
	_, second, _, err := Normalize(in, now, api.PlanHobby.IssueLimits())
	if err != nil || first != second {
		t.Fatalf("unstable grouping: %s %s %v", first, second, err)
	}
	in.Frames[0].Function = "sendExport"
	_, different, _, err := Normalize(in, now, api.PlanHobby.IssueLimits())
	if err != nil || first == different {
		t.Fatal("distinct failure site merged")
	}
}
func TestIssueSanitizesEvidence(t *testing.T) {
	now := time.Now().UTC()
	in := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", Message: "alice@example.com 4111111111111111", StackTrace: "customer bob@example.com", Frames: []api.IssueFrame{{File: "alice@example.com", Function: "f", InApp: true}}}
	out, _, title, err := Normalize(in, now, api.PlanHobby.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{out.Message, out.StackTrace, title, out.Frames[0].File} {
		if strings.Contains(v, "@example.com") || strings.Contains(v, "4111111111111111") {
			t.Fatalf("sensitive text retained: %s", v)
		}
	}
	if len(out.Redactions) == 0 {
		t.Fatal("redaction disclosure missing")
	}
}
func TestIssueRecurrenceExcludesOldAndLateEvents(t *testing.T) {
	now := time.Now()
	in := api.Issue{State: "resolved", ResolvedAt: &now, FixedDeploymentID: "v43"}
	if Recurs(in, "v42", now.Add(time.Minute)) || Recurs(in, "v43", now.Add(-time.Minute)) || !Recurs(in, "v43", now.Add(time.Minute)) {
		t.Fatal("incorrect release-aware recurrence")
	}
}

func TestIssueGroupingPreservesDistinctSourceDirectories(t *testing.T) {
	now := time.Now().UTC()
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", Frames: []api.IssueFrame{{File: "/app/billing/export.ts", Function: "run", InApp: true}}}
	_, one, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	event.Frames[0].File = "/app/reports/export.ts"
	_, two, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
	if err != nil || one == two {
		t.Fatal("same filename collapsed distinct modules")
	}
}

func TestIssueOpaqueRequestIDsAndSensitiveContext(t *testing.T) {
	now := time.Now().UTC()
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", RequestID: "customer-request-42"}
	out, _, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
	if err != nil || out.RequestID != event.RequestID {
		t.Fatal("valid gateway request ID lost")
	}
	event.RequestID = "alice@example.com"
	out, _, _, err = Normalize(event, now, api.PlanHobby.IssueLimits())
	if err != nil || strings.Contains(out.RequestID, "@example.com") {
		t.Fatal("sensitive request context retained")
	}
}
