package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeIssueOwnershipRules(t *testing.T) {
	assignee := uuid.NewString()
	got, err := NormalizeIssueOwnershipRules([]IssueOwnershipRule{{
		ExceptionType: " DateFormatError ", SourceKind: " exception ", RoutePrefix: " /exports ", AssigneeAccountID: strings.ToUpper(assignee),
	}})
	if err != nil {
		t.Fatalf("NormalizeIssueOwnershipRules: %v", err)
	}
	if len(got) != 1 || got[0].ExceptionType != "DateFormatError" || got[0].SourceKind != "exception" || got[0].RoutePrefix != "/exports" || got[0].AssigneeAccountID != assignee {
		t.Fatalf("normalized rules = %+v", got)
	}
	for name, rules := range map[string][]IssueOwnershipRule{
		"empty matcher":        {{AssigneeAccountID: assignee}},
		"invalid source":       {{SourceKind: "timer", AssigneeAccountID: assignee}},
		"relative route":       {{RoutePrefix: "exports", AssigneeAccountID: assignee}},
		"query route":          {{RoutePrefix: "/exports?all", AssigneeAccountID: assignee}},
		"invalid assignee":     {{SourceKind: "worker", AssigneeAccountID: "account-1"}},
		"oversized collection": make([]IssueOwnershipRule, IssueOwnershipRulesMax+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeIssueOwnershipRules(rules); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
