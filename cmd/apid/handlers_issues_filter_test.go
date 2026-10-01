package main

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestParseIssueListFilter(t *testing.T) {
	accountID := uuid.NewString()
	ownerID := uuid.NewString()
	tests := []struct {
		name      string
		query     url.Values
		want      state.IssueListFilter
		wantError bool
	}{
		{name: "all issues", query: url.Values{}, want: state.IssueListFilter{}},
		{name: "mine", query: url.Values{"state": {"open"}, "environment": {"staging"}, "assignee": {"me"}}, want: state.IssueListFilter{State: "open", Environment: "staging", AssigneeAccountID: accountID}},
		{name: "unassigned", query: url.Values{"assignee": {"unassigned"}}, want: state.IssueListFilter{Unassigned: true}},
		{name: "specific owner", query: url.Values{"assignee": {ownerID}}, want: state.IssueListFilter{AssigneeAccountID: ownerID}},
		{name: "invalid owner", query: url.Values{"assignee": {"team"}}, wantError: true},
		{name: "invalid state", query: url.Values{"state": {"closed"}}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/issues?"+tt.query.Encode(), nil)
			got, err := parseIssueListFilter(r, accountID)
			if (err != nil) != tt.wantError {
				t.Fatalf("parseIssueListFilter error = %v, wantError %v", err, tt.wantError)
			}
			if err == nil && got != tt.want {
				t.Fatalf("parseIssueListFilter = %+v, want %+v", got, tt.want)
			}
		})
	}
}
