package issues

import (
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
)

func TestOwnershipRuleSourcePathsTagsAndCauseFrames(t *testing.T) {
	rule := api.IssueOwnershipRule{ExceptionType: "PaymentError", SourceKind: "exception", SourcePathPrefix: "/app/src/payments", Tag: "component=payments"}
	base := api.IssueEvent{ExceptionType: "PaymentError", SourceKind: "exception", Tags: map[string]string{"component": "payments"}}
	for _, tc := range []struct {
		name  string
		frame api.IssueFrame
		cause bool
		match bool
	}{
		{"application source", api.IssueFrame{File: "/app/src/payments/charge.ts", InApp: true}, false, true},
		{"adjacent directory", api.IssueFrame{File: "/app/src/payments-admin/charge.ts", InApp: true}, false, false},
		{"dependency", api.IssueFrame{File: "/app/src/payments/charge.ts", InApp: false}, false, false},
		{"cause frame", api.IssueFrame{File: "file:///app/src/payments/charge.ts", InApp: true}, true, true},
		{"wide source path", api.IssueFrame{File: "/app/src/payments/" + strings.Repeat("a", 300) + ".ts", InApp: true}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := base
			if tc.cause {
				event.Causes = []api.IssueCause{{Frames: []api.IssueFrame{tc.frame}}}
			} else {
				event.Frames = []api.IssueFrame{tc.frame}
			}
			if got := OwnershipRuleMatches(rule, event); got != tc.match {
				t.Fatalf("match=%v want=%v", got, tc.match)
			}
		})
	}
	event := base
	event.Frames = []api.IssueFrame{{File: "/app/src/payments/charge.ts", InApp: true}}
	event.Tags = map[string]string{"component": "exports"}
	if OwnershipRuleMatches(rule, event) {
		t.Fatal("tag and source matchers were ORed")
	}
	rule = api.IssueOwnershipRule{Tag: "component="}
	if OwnershipRuleMatches(rule, api.IssueEvent{}) {
		t.Fatal("missing tag matched an empty reported value")
	}
	if !OwnershipRuleMatches(rule, api.IssueEvent{Tags: map[string]string{"component": ""}}) {
		t.Fatal("explicit empty value failed to match")
	}
}
