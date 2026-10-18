package issues

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReportedContextDoesNotChangeGrouping(t *testing.T) {
	now := time.Now().UTC()
	base := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", Message: "failed", Frames: []api.IssueFrame{{File: "worker.go", Function: "run", InApp: true}}}
	_, want, _, err := Normalize(base, now, api.PlanPro.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	base.Tags = map[string]string{"component": "worker", "email": "private@example.com"}
	base.Breadcrumbs = []api.IssueBreadcrumb{{OccurredAt: now.Add(-2 * time.Hour), Category: "custom", Level: "info", Message: "stale"}, {OccurredAt: now, Category: "workflow", Level: "info", Message: strings.Repeat("x", 300)}}
	got, fp, _, err := Normalize(base, now, api.PlanPro.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	if fp != want || len(got.Tags) != 1 || got.Tags["component"] != "worker" || len(got.Breadcrumbs) != 1 || len(got.Breadcrumbs[0].Message) != 256 || got.BreadcrumbsDropped != 1 {
		t.Fatalf("unexpected context: %+v", got)
	}
	found := false
	for _, marker := range got.Redactions {
		if marker == "issue_tag_allowlist" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing allowlist redaction marker")
	}
}
func TestReportedContextRejectsMalformedEvidence(t *testing.T) {
	now := time.Now().UTC()
	for _, crumb := range []api.IssueBreadcrumb{{OccurredAt: now, Category: "headers", Level: "info"}, {OccurredAt: now, Category: "custom", Level: "fatal"}, {OccurredAt: now.Add(time.Hour), Category: "custom", Level: "info"}, {OccurredAt: now, Category: "custom", Level: "info", Message: "nul\x00"}} {
		in := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", Message: "failed", Breadcrumbs: []api.IssueBreadcrumb{crumb}}
		if _, _, _, err := Normalize(in, now, api.PlanPro.IssueLimits()); err == nil {
			t.Fatalf("accepted %+v", crumb)
		}
	}
}

func TestOTLPReportedContextUsesSameValidationAndStableRetryIdentity(t *testing.T) {
	now := time.Now().UTC()
	crumbs, _ := json.Marshal([]api.IssueBreadcrumb{{OccurredAt: now, Category: "workflow", Level: "info", Message: "starting charge"}})
	attrs := []map[string]any{}
	for key, value := range map[string]string{"exception.type": "Error", "exception.message": "failed", "gregale.issue.tag.component": "payments", "gregale.issue.tag.email": "private@example.com", "gregale.issue.breadcrumbs": string(crumbs), "gregale.issue.breadcrumbs_dropped": "2"} {
		attrs = append(attrs, map[string]any{"key": key, "value": map[string]string{"stringValue": value}})
	}
	raw, _ := json.Marshal(map[string]any{"resourceLogs": []any{map[string]any{"scopeLogs": []any{map[string]any{"logRecords": []any{map[string]any{"timeUnixNano": now.UnixNano(), "attributes": attrs}}}}}}})
	events, err := ExtractOTLP(raw, "logs")
	if err != nil || len(events) != 1 {
		t.Fatalf("extract: %v", err)
	}
	retry, err := ExtractOTLP(raw, "logs")
	if err != nil || retry[0].EventID != events[0].EventID {
		t.Fatal("unstable retry identity")
	}
	event, _, _, err := Normalize(events[0], now, api.PlanPro.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(event.Tags) != 1 || event.Tags["component"] != "payments" || len(event.Breadcrumbs) != 1 || event.BreadcrumbsDropped != 2 {
		t.Fatalf("context: %+v", event)
	}
}

func TestReportedContextUTF8LimitsAfterRedaction(t *testing.T) {
	now := time.Now().UTC()
	in := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", Message: "failed", Tags: map[string]string{"component": strings.Repeat("€", 100)}, Breadcrumbs: []api.IssueBreadcrumb{{OccurredAt: now, Category: "custom", Level: "info", Message: strings.Repeat("€", 100)}}}
	event, _, _, err := Normalize(in, now, api.PlanPro.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(event.Tags["component"]) > 128 || !utf8.ValidString(event.Tags["component"]) || len(event.Breadcrumbs[0].Message) > 256 || !utf8.ValidString(event.Breadcrumbs[0].Message) {
		t.Fatal("context byte or UTF-8 boundary exceeded")
	}
}
