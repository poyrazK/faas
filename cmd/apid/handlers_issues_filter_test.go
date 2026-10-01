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
		{name: "all issues", query: url.Values{}, want: state.IssueListFilter{Sort: "recent"}},
		{name: "mine", query: url.Values{"state": {"open"}, "environment": {"staging"}, "assignee": {"me"}}, want: state.IssueListFilter{State: "open", Environment: "staging", AssigneeAccountID: accountID, Sort: "recent"}},
		{name: "unassigned", query: url.Values{"assignee": {"unassigned"}}, want: state.IssueListFilter{Unassigned: true, Sort: "recent"}},
		{name: "specific owner", query: url.Values{"assignee": {ownerID}}, want: state.IssueListFilter{AssigneeAccountID: ownerID, Sort: "recent"}},
		{name: "impact order and threshold", query: url.Values{"sort": {"impact"}, "min_customers": {"3"}}, want: state.IssueListFilter{Sort: "impact", MinCustomers: 3}},
		{name: "recent order with threshold", query: url.Values{"sort": {"recent"}, "min_customers": {"2"}}, want: state.IssueListFilter{Sort: "recent", MinCustomers: 2}},
		{name: "invalid owner", query: url.Values{"assignee": {"team"}}, wantError: true},
		{name: "invalid state", query: url.Values{"state": {"closed"}}, wantError: true},
		{name: "invalid sort", query: url.Values{"sort": {"customers"}}, wantError: true},
		{name: "invalid customer threshold", query: url.Values{"min_customers": {"-1"}}, wantError: true},
		{name: "malformed customer threshold", query: url.Values{"min_customers": {"many"}}, wantError: true},
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
