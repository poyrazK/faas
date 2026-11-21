package issues

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// An error group recorded from gateway responses is titled by status and
// route, not "unhandled: /" (prod findings 2026-11-15 #37), and the new title
// leaves its fingerprint alone.
func TestNormalize_HTTPIssueTitle(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name  string
		event api.IssueEvent
		want  string
	}{
		{"generic 5xx", api.IssueEvent{ExceptionType: "unhandled", Message: "/", Route: "/", HTTPStatus: 500}, "HTTP 500 Internal Server Error on /"},
		{"route template over path", api.IssueEvent{ExceptionType: "unhandled", Message: "/cart/42", Route: "/cart/{id}", HTTPStatus: 502}, "HTTP 502 Bad Gateway on /cart/{id}"},
		{"client error", api.IssueEvent{ExceptionType: "client_error", Message: "/missing", HTTPStatus: 404}, "HTTP 404 Not Found on /missing"},
		{"specific class kept", api.IssueEvent{ExceptionType: "wake_failed", Route: "/", HTTPStatus: 503}, "HTTP 503 Service Unavailable on / (wake_failed)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.event
			in.EventID, in.OccurredAt, in.SourceKind = uuid.NewString(), now, "http"
			_, fp, title, err := Normalize(in, now, api.PlanPro.IssueLimits())
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if title != tt.want {
				t.Fatalf("title = %q, want %q", title, tt.want)
			}
			// The title is display only: the group's fingerprint is what an
			// event before this change got.
			decision := decideGrouping(in, GroupingVersionFor(in))
			if want := groupingFingerprint(decision.parts, decision.explanation.GroupingVersion); fp != want {
				t.Fatalf("fingerprint changed: %s, want %s", fp, want)
			}
		})
	}
	// Non-HTTP titles are unchanged.
	exception := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "TypeError", Message: "x is undefined", SourceKind: "exception"}
	if _, _, title, err := Normalize(exception, now, api.PlanPro.IssueLimits()); err != nil || title != "TypeError: x is undefined" {
		t.Fatalf("exception title = %q, %v; want unchanged", title, err)
	}
}
