package api

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestIssueSavedViewValidationAndViewerRelativeQuery(t *testing.T) {
	in := PutIssueSavedViewRequest{Name: "  My payments  ", Filters: IssueSavedViewFilters{State: "open", Assignee: "me", Tag: "component=payments"}}
	out, err := NormalizeIssueSavedView(in)
	if err != nil || out.Name != "My payments" || out.Filters.Sort != "recent" || out.Filters.Query().Get("assignee") != "me" || out.Filters.Query().Get("cursor") != "" {
		t.Fatalf("viewer-relative view: %+v %v", out, err)
	}
	for _, tc := range []PutIssueSavedViewRequest{
		{Name: strings.Repeat("€", 22)}, {Name: "bad\nname"}, {Name: "bad\x00name"}, {Name: "invalid\xff"},
		{Name: "valid", Filters: IssueSavedViewFilters{State: "unknown"}},
		{Name: "valid", Filters: IssueSavedViewFilters{Environment: "../other"}},
		{Name: "valid", Filters: IssueSavedViewFilters{Assignee: uuid.Nil.String()}},
		{Name: "valid", Filters: IssueSavedViewFilters{MinCustomers: -1}},
		{Name: "valid", Filters: IssueSavedViewFilters{Sort: "sql"}},
		{Name: "valid", Filters: IssueSavedViewFilters{Tag: "email=private@example.com"}},
	} {
		if _, err := NormalizeIssueSavedView(tc); err == nil {
			t.Fatalf("invalid view accepted: %+v", tc)
		}
	}
	for _, key := range []string{"", "Upper", "../other", strings.Repeat("a", IssueSavedViewKeyMaxBytes+1)} {
		if ValidateIssueSavedViewKey(key) == nil {
			t.Fatalf("invalid key accepted: %q", key)
		}
	}
	if err := ValidateIssueSavedViewKey(strings.Repeat("a", IssueSavedViewKeyMaxBytes)); err != nil {
		t.Fatal(err)
	}
}

func TestExtendedIssueOwnershipRuleValidation(t *testing.T) {
	owner := uuid.NewString()
	out, err := NormalizeIssueOwnershipRules([]IssueOwnershipRule{{SourcePathPrefix: ` .\src\payments\ `, Tag: "component=payments", AssigneeAccountID: owner}})
	if err != nil || out[0].SourcePathPrefix != "src/payments" {
		t.Fatalf("path normalization: %+v %v", out, err)
	}
	for _, rule := range []IssueOwnershipRule{
		{SourcePathPrefix: "../secrets", AssigneeAccountID: owner},
		{SourcePathPrefix: "src/../secrets", AssigneeAccountID: owner},
		{SourcePathPrefix: "https://example.test/src", AssigneeAccountID: owner},
		{SourcePathPrefix: "src?private", AssigneeAccountID: owner},
		{Tag: "email=private@example.com", AssigneeAccountID: owner},
	} {
		if _, err := NormalizeIssueOwnershipRules([]IssueOwnershipRule{rule}); err == nil {
			t.Fatalf("invalid routing accepted: %+v", rule)
		}
	}
}
