package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestIssueQuotaResponseRetryability(t *testing.T) {
	for _, tc := range []struct {
		resource    string
		rate        bool
		status      int
		retry, code string
	}{
		{"events", false, http.StatusTooManyRequests, "60", "issue_quota_exceeded"},
		{"events_per_minute", true, http.StatusTooManyRequests, "60", "issue_rate_limited"},
		{"issues", false, http.StatusConflict, "", "issue_quota_exceeded"},
	} {
		t.Run(tc.resource, func(t *testing.T) {
			w := httptest.NewRecorder()
			apidserver.WriteIssueErrorForTest(w, &state.IssueLimitError{Resource: tc.resource, Limit: 1, Observed: 2, Rate: tc.rate})
			var problem api.Problem
			if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || w.Header().Get("Retry-After") != tc.retry || problem.Code != tc.code {
				t.Fatalf("status=%d retry=%q problem=%+v", w.Code, w.Header().Get("Retry-After"), problem)
			}
		})
	}
}

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
			got, err := apidserver.ParseIssueListFilter(r, accountID)
			if (err != nil) != tt.wantError {
				t.Fatalf("parseIssueListFilter error = %v, wantError %v", err, tt.wantError)
			}
			if err == nil && got != tt.want {
				t.Fatalf("parseIssueListFilter = %+v, want %+v", got, tt.want)
			}
		})
	}
}
