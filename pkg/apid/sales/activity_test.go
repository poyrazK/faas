package sales

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

type activityFake struct {
	*fakeStore
	input   referrals.ActivityInput
	filter  referrals.ActivityFilter
	alerts  referrals.AlertFilter
	entries []referrals.Activity
	calls   int
}

func (f *activityFake) LeadActivity(_ context.Context, scope referrals.Scope, lead string, filter referrals.ActivityFilter) (referrals.Prospect, []referrals.Activity, error) {
	f.scope = scope
	f.filter = filter
	f.calls++
	return referrals.Prospect{ID: lead, ProspectInput: referrals.ProspectInput{Version: 1}}, f.entries, nil
}
func (f *activityFake) RecordActivity(_ context.Context, scope referrals.Scope, _ string, in referrals.ActivityInput) (string, error) {
	f.scope = scope
	f.input = in
	f.calls++
	return in.ID, nil
}
func (f *activityFake) LeadAlerts(_ context.Context, scope referrals.Scope, filter referrals.AlertFilter) ([]referrals.LeadAlert, referrals.AlertCounts, error) {
	f.scope = scope
	f.alerts = filter
	f.calls++
	return []referrals.LeadAlert{}, referrals.AlertCounts{}, nil
}
func activityService() (*Service, *activityFake, state.Account) {
	s, base, acct := serviceFixture()
	f := &activityFake{fakeStore: base, entries: []referrals.Activity{}}
	s.Store = f
	return s, f, acct
}
func TestSalesActivityRequiresVerifiedStaffMFA(t *testing.T) {
	for _, change := range []func(*Service, *state.Account){func(s *Service, _ *state.Account) { s.Config.Enabled = false }, func(_ *Service, a *state.Account) { a.Email = "outsider@example.com" }, func(_ *Service, a *state.Account) { a.EmailVerifiedAt = nil }, func(_ *Service, a *state.Account) { a.MFAEnrolledAt = nil }} {
		s, f, acct := activityService()
		change(s, &acct)
		for _, h := range []func(http.ResponseWriter, *http.Request, state.Account){s.Activities, s.RecordActivity, s.Alerts} {
			w := httptest.NewRecorder()
			h(w, httptest.NewRequest("GET", "/v1/sales/alerts", nil), acct)
			if (w.Code != 403 && w.Code != 404) || f.calls != 0 {
				t.Fatal(w.Code, f.calls)
			}
		}
	}
}
func TestSalesActivityValidationAndNormalization(t *testing.T) {
	s, f, acct := activityService()
	base := referrals.ActivityInput{ID: uuid.NewString(), LeadVersion: 1, Kind: "demo", Summary: "  Walked through deployment  ", Outcome: " Agreed on a pilot ", NextStep: " Follow up ", OccurredAt: time.Now().UTC().Add(-time.Hour), FollowUpTitle: " Pilot scope ", FollowUpOn: "2026-11-06"}
	send := func(in referrals.ActivityInput) int {
		raw, _ := json.Marshal(in)
		r := httptest.NewRequest("POST", "/v1/sales/leads/x/activities", strings.NewReader(string(raw)))
		r.SetPathValue("lead", acct.ID)
		w := httptest.NewRecorder()
		s.RecordActivity(w, r, acct)
		return w.Code
	}
	if status := send(base); status != 200 || f.input.Summary != "Walked through deployment" || f.input.Outcome != "Agreed on a pilot" || f.input.FollowUpTitle != "Pilot scope" {
		t.Fatal(status, f.input)
	}
	for _, tc := range []struct {
		name   string
		change func(*referrals.ActivityInput)
	}{{"kind", func(in *referrals.ActivityInput) { in.Kind = "payment" }}, {"future", func(in *referrals.ActivityInput) { in.OccurredAt = time.Now().Add(time.Hour) }}, {"old", func(in *referrals.ActivityInput) { in.OccurredAt = time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC) }}, {"no id", func(in *referrals.ActivityInput) { in.ID = "" }}, {"version", func(in *referrals.ActivityInput) { in.LeadVersion = 0 }}, {"multibyte", func(in *referrals.ActivityInput) { in.Summary = strings.Repeat("界", 334) }}, {"outcome", func(in *referrals.ActivityInput) { in.Outcome = strings.Repeat("x", api.SalesActivityDetailMaxBytes+1) }}, {"unpaired task", func(in *referrals.ActivityInput) { in.FollowUpTitle = "" }}, {"invalid task day", func(in *referrals.ActivityInput) { in.FollowUpOn = "1999-01-01" }}} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.change(&in)
			before := f.calls
			if status := send(in); status != 400 || f.calls != before {
				t.Fatal(status, f.calls)
			}
		})
	}
}
func TestSalesActivityCursorBindsLeadOwnerAndOrdering(t *testing.T) {
	s, f, acct := activityService()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	for range api.SalesPageSize + 1 {
		f.entries = append(f.entries, referrals.Activity{ActivityInput: referrals.ActivityInput{ID: uuid.NewString(), OccurredAt: at}})
	}
	send := func(lead, after string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/sales/leads/"+lead+"/activities?after="+after, nil)
		r.SetPathValue("lead", lead)
		w := httptest.NewRecorder()
		s.Activities(w, r, acct)
		return w
	}
	w := send(acct.ID, "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Next string               `json:"next_cursor"`
		Rows []referrals.Activity `json:"activities"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Next == "" || len(result.Rows) != api.SalesPageSize {
		t.Fatal(result)
	}
	f.entries = nil
	w = send(acct.ID, result.Next)
	if w.Code != 200 || !f.filter.AfterAt.Equal(at) || f.filter.AfterID != result.Rows[len(result.Rows)-1].ID {
		t.Fatal(w.Code, f.filter)
	}
	before := f.calls
	if w = send(uuid.NewString(), result.Next); w.Code != 400 || f.calls != before {
		t.Fatal("cursor crossed lead", w.Code)
	}
	s.Config.Members[acct.Email] = true
	if w = send(acct.ID, result.Next); w.Code != 400 {
		t.Fatal("cursor crossed manager scope", w.Code)
	}
}
func TestSalesAttentionFiltersAndConfiguration(t *testing.T) {
	s, f, acct := activityService()
	request := func(query string) int {
		w := httptest.NewRecorder()
		s.Alerts(w, httptest.NewRequest("GET", "/v1/sales/alerts"+query, nil), acct)
		return w.Code
	}
	if code := request("?kind=stale&on=2026-11-04"); code != 200 || f.alerts.StaleDays != api.SalesStaleDefaultDays || f.scope.All || f.alerts.On != "2026-11-04" {
		t.Fatal(code, f.alerts, f.scope)
	}
	for _, query := range []string{"?kind=unknown", "?on=1999-01-01", "?owner=" + uuid.NewString(), "?after=invalid"} {
		before := f.calls
		if code := request(query); code != 400 || f.calls != before {
			t.Fatal(query, code, f.calls)
		}
	}
	s.Config.Members[acct.Email] = true
	s.Config.StaleDays = 7
	if code := request("?owner=" + uuid.NewString()); code != 200 || !f.scope.All || f.alerts.StaleDays != 7 {
		t.Fatal(code, f.scope, f.alerts)
	}
	for _, value := range []string{"0", "366", "invalid"} {
		_, err := FromEnv(func(k string) string {
			if k == "FAAS_SALES_ENABLED" {
				return "1"
			}
			if k == "FAAS_SALES_STALE_DAYS" {
				return value
			}
			return ""
		}, "")
		if err == nil {
			t.Fatal("invalid stale threshold accepted", value)
		}
	}
	c, err := FromEnv(func(k string) string {
		if k == "FAAS_SALES_ENABLED" {
			return "1"
		}
		if k == "FAAS_SALES_STALE_DAYS" {
			return "7"
		}
		return ""
	}, "")
	if err != nil || c.StaleDays != 7 {
		t.Fatal(c, err)
	}
}
