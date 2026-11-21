package sales

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type qualificationFake struct {
	*fakeStore
	calls  int
	input  referrals.QualificationInput
	filter referrals.QualificationFilter
	rows   []referrals.QualificationGap
}

func (f *qualificationFake) QualificationProfile(_ context.Context, s referrals.Scope, id string) (referrals.QualificationProfile, error) {
	f.calls++
	f.scope = s
	return referrals.QualificationProfile{Lead: referrals.Prospect{ID: id, ProspectInput: referrals.ProspectInput{Version: 1}}}, nil
}
func (f *qualificationFake) SaveQualification(_ context.Context, s referrals.Scope, _ string, in referrals.QualificationInput) error {
	f.calls++
	f.scope = s
	f.input = in
	return nil
}
func (f *qualificationFake) QualificationOverview(_ context.Context, s referrals.Scope, filter referrals.QualificationFilter) ([]referrals.QualificationGap, referrals.QualificationCounts, []referrals.OutcomeCount, error) {
	f.calls++
	f.scope = s
	f.filter = filter
	return f.rows, referrals.QualificationCounts{}, []referrals.OutcomeCount{}, nil
}
func qualificationService() (*Service, *qualificationFake, state.Account) {
	s, base, acct := serviceFixture()
	f := &qualificationFake{fakeStore: base}
	s.Store = f
	return s, f, acct
}
func TestQualificationAccessAndManagerGuard(t *testing.T) {
	for _, change := range []func(*Service, *state.Account){func(s *Service, _ *state.Account) { s.Config.Enabled = false }, func(_ *Service, a *state.Account) { a.Email = "outsider@example.com" }, func(_ *Service, a *state.Account) { a.EmailVerifiedAt = nil }, func(_ *Service, a *state.Account) { a.MFAEnrolledAt = nil }} {
		s, f, acct := qualificationService()
		change(s, &acct)
		for _, h := range []func(http.ResponseWriter, *http.Request, state.Account){s.Qualification, s.SaveQualification, s.QualificationOverview} {
			w := httptest.NewRecorder()
			h(w, httptest.NewRequest("GET", "/v1/sales/qualification", nil), acct)
			if (w.Code != 403 && w.Code != 404) || f.calls != 0 {
				t.Fatal(w.Code, f.calls)
			}
		}
	}
	s, f, acct := qualificationService()
	w := httptest.NewRecorder()
	s.QualificationOverview(w, httptest.NewRequest("GET", "/v1/sales/qualification", nil), acct)
	if w.Code != 403 || f.calls != 0 {
		t.Fatal("rep read manager overview", w.Code, f.calls)
	}
}
func TestQualificationInputAndDefaults(t *testing.T) {
	s, f, acct := qualificationService()
	base := referrals.QualificationInput{LeadVersion: 1, UseCase: "  Customer API  ", Workload: "Node services", DecisionMaker: "CTO", BuyingTimeline: "date", DecisionOn: "2026-12-01", Priority: "high", ProductFit: "strong", Blocker: "  Pilot review  "}
	send := func(in referrals.QualificationInput) int {
		raw, _ := json.Marshal(in)
		r := httptest.NewRequest("PUT", "/v1/sales/leads/x/qualification", strings.NewReader(string(raw)))
		r.SetPathValue("lead", acct.ID)
		w := httptest.NewRecorder()
		s.SaveQualification(w, r, acct)
		return w.Code
	}
	if code := send(base); code != 200 || f.input.UseCase != "Customer API" || f.input.Blocker != "Pilot review" || f.scope.All {
		t.Fatal(code, f.input, f.scope)
	}
	for _, tc := range []struct {
		name   string
		change func(*referrals.QualificationInput)
	}{{"parent version", func(i *referrals.QualificationInput) { i.LeadVersion = 0 }}, {"version", func(i *referrals.QualificationInput) { i.Version = -1 }}, {"bytes", func(i *referrals.QualificationInput) { i.Workload = strings.Repeat("界", 334) }}, {"maker", func(i *referrals.QualificationInput) { i.DecisionMaker = strings.Repeat("a", 201) }}, {"timeline", func(i *referrals.QualificationInput) { i.BuyingTimeline = "guaranteed" }}, {"priority", func(i *referrals.QualificationInput) { i.Priority = "automatic" }}, {"fit", func(i *referrals.QualificationInput) { i.ProductFit = "perfect" }}, {"date pairing", func(i *referrals.QualificationInput) { i.BuyingTimeline = "30_days" }}, {"invalid date", func(i *referrals.QualificationInput) { i.DecisionOn = "2026-02-30" }}} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.change(&in)
			before := f.calls
			if code := send(in); code != 400 || f.calls != before {
				t.Fatal(code, f.calls)
			}
		})
	}
	if code := send(referrals.QualificationInput{LeadVersion: 1}); code != 200 || f.input.BuyingTimeline != "unknown" || f.input.Priority != "unknown" || f.input.ProductFit != "unknown" {
		t.Fatal(code, f.input)
	}
}
func TestQualificationOverviewCursorsAndScope(t *testing.T) {
	s, f, acct := qualificationService()
	s.Config.Members[acct.Email] = true
	for range api.SalesPageSize + 1 {
		f.rows = append(f.rows, referrals.QualificationGap{ID: uuid.NewString(), Missing: []string{"use_case"}})
	}
	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.QualificationOverview(w, httptest.NewRequest("GET", "/v1/sales/qualification"+query, nil), acct)
		return w
	}
	w := get("")
	var result struct {
		Next string                       `json:"next_cursor"`
		Rows []referrals.QualificationGap `json:"leads"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Next == "" || len(result.Rows) != api.SalesPageSize {
		t.Fatal(w.Code, w.Body.String())
	}
	f.rows = nil
	w = get("?after=" + result.Next)
	if w.Code != 200 || f.filter.After != result.Rows[len(result.Rows)-1].ID || !f.scope.All {
		t.Fatal(w.Code, f.filter, f.scope)
	}
	if code := get("?owner=" + uuid.NewString() + "&after=" + result.Next).Code; code != 400 {
		t.Fatal("cursor crossed owner filter", code)
	}
}
func TestClosingReasonRequestValidation(t *testing.T) {
	s, f, acct := pipelineService()
	for _, tc := range []struct {
		stage, reason, note string
		want                int
	}{{"won", "", "", 400}, {"won", "product_fit", "Fits the workload", 200}, {"lost", "budget", "", 200}, {"lost", "product_fit", "", 400}, {"won", "other", "", 400}, {"won", "other", "Pilot succeeded", 200}, {"new", "budget", "", 400}} {
		body := leadBody(acct.ID)
		body = strings.Replace(body, `"stage":"new"`, `"stage":"`+tc.stage+`"`, 1)
		body = strings.TrimSuffix(body, "}") + `,"close_reason":"` + tc.reason + `","close_note":"` + tc.note + `"}`
		w := httptest.NewRecorder()
		s.SaveProspect(w, httptest.NewRequest("POST", "/v1/sales/leads", strings.NewReader(body)), acct)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String(), f.calls)
		}
	}
}
