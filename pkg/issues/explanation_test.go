package issues

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1112
func TestGroupingExplanationSelectedEvidence(t *testing.T) {
	now := time.Now().UTC()
	base := api.IssueEvent{
		EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error",
		Message: " customer 123\n failed ", Route: "/checkout", HTTPStatus: 500,
		Frames: []api.IssueFrame{
			{File: "/app/dependency.js", Function: "library"},
			{File: "/build/release43/src/checkout.ts", Function: "charge", Line: 99, InApp: true},
			{File: "/app/handler.ts", Function: "post", InApp: true},
		},
	}
	cases := []struct {
		name   string
		change func(*api.IssueEvent)
		basis  string
		frames []api.IssueGroupingFrame
		kind   string
		value  string
	}{
		{"legacy frames", func(*api.IssueEvent) {}, "application-frames", []api.IssueGroupingFrame{
			{FrameIndex: 1, File: "src/checkout.ts", Function: "charge"},
			{FrameIndex: 2, File: "handler.ts", Function: "post"},
		}, "none", ""},
		{"failure site", func(e *api.IssueEvent) { e.GroupingStrategy = FailureSiteStrategy }, "failure-site",
			[]api.IssueGroupingFrame{{FrameIndex: 1, File: "src/checkout.ts", Function: "charge"}}, "message", "customer <value> failed"},
		{"python order", func(e *api.IssueEvent) {
			e.GroupingStrategy, e.FrameOrder, e.ErrorCode = FailureSiteStrategy, "oldest-first", "PAYMENT_404"
		}, "failure-site", []api.IssueGroupingFrame{{FrameIndex: 2, File: "handler.ts", Function: "post"}}, "error-code", "PAYMENT_404"},
		{"anonymous site", func(e *api.IssueEvent) {
			e.GroupingStrategy, e.Frames[1].Function = FailureSiteStrategy, " "
		}, "failure-site", []api.IssueGroupingFrame{{FrameIndex: 1, File: "src/checkout.ts", Function: "<anonymous>"}}, "message", "customer <value> failed"},
		{"legacy fallback", func(e *api.IssueEvent) { e.Frames = nil }, "fallback", nil, "message", " customer <value>\n failed "},
		{"failure site fallback", func(e *api.IssueEvent) {
			e.GroupingStrategy, e.Frames = FailureSiteStrategy, []api.IssueFrame{{InApp: true}}
		}, "fallback", nil, "message", "customer <value> failed"},
		{"override", func(e *api.IssueEvent) {
			e.GroupingStrategy, e.FingerprintOverride, e.ErrorCode = FailureSiteStrategy, "payment", "IGNORED"
		}, "override", nil, "override", "payment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := base
			event.Frames = append([]api.IssueFrame(nil), base.Frames...)
			tc.change(&event)
			normalized, fp, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
			if err != nil {
				t.Fatal(err)
			}
			version := GroupingVersionFor(normalized)
			explanation := ExplainGrouping(normalized, version, fp)
			if explanation == nil {
				t.Fatal("normalized evidence did not verify")
			}
			if explanation.Basis != tc.basis || !reflect.DeepEqual(explanation.Frames, tc.frames) ||
				explanation.DiscriminatorKind != tc.kind || explanation.DiscriminatorValue != tc.value {
				t.Fatalf("wrong selected evidence: %+v", explanation)
			}
			if explanation.SourceKind != "exception" || explanation.ExceptionType != "Error" ||
				explanation.GroupingVersion != version || explanation.Strategy != map[int]string{1: "legacy", 2: FailureSiteStrategy}[version] {
				t.Fatalf("wrong common identity: %+v", explanation)
			}
			if tc.basis == "fallback" {
				if explanation.Route != "/checkout" || explanation.HTTPStatus != 500 {
					t.Fatal("missing fallback identity")
				}
			} else if explanation.Route != "" || explanation.HTTPStatus != 0 {
				t.Fatal("irrelevant route/status exposed as identity")
			}
			// Stored version is authoritative even if a historic envelope's
			// strategy label is absent or misleading. Time is not revalidated.
			normalized.GroupingStrategy, normalized.OccurredAt = "unsupported-future-label", now.AddDate(-1, 0, 0)
			if ExplainGrouping(normalized, version, fp) == nil {
				t.Fatal("retained history was revalidated")
			}
			if ExplainGrouping(normalized, 99, fp) != nil || ExplainGrouping(normalized, version, strings.Repeat("0", 64)) != nil {
				t.Fatal("unsupported or mismatched identity was explained")
			}
		})
	}
}

func TestGroupingExplanationUsesOnlySanitizedEvidence(t *testing.T) {
	now := time.Now().UTC()
	event := api.IssueEvent{
		EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error",
		Message: "failed for person@example.com", GroupingStrategy: FailureSiteStrategy,
		RequestID: "unrelated-request", TraceID: strings.Repeat("a", 32),
		Frames: []api.IssueFrame{{File: "/app/person@example.com.ts", Function: "charge", InApp: true}},
	}
	normalized, fp, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	explanation := ExplainGrouping(normalized, FailureSiteGroupingVersion, fp)
	if explanation == nil {
		t.Fatal("sanitized evidence did not verify")
	}
	raw, err := json.Marshal(explanation)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"person@example.com", "unrelated-request", event.TraceID, "stack_trace", "line", "request_id"} {
		if strings.Contains(string(raw), excluded) {
			t.Fatalf("irrelevant or raw evidence exposed: %s", excluded)
		}
	}
}
