package sales

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

type pipelineFake struct {
	*fakeStore
	team      []referrals.Rep
	leads     []referrals.Prospect
	tasks     []referrals.Task
	saved     referrals.ProspectInput
	taskInput referrals.TaskInput
	failure   error
	calls     int
}

func (f *pipelineFake) SalesTeam(context.Context, []string) ([]referrals.Rep, error) {
	return f.team, nil
}
func (f *pipelineFake) Prospects(_ context.Context, s referrals.Scope, _ referrals.PipelineFilter) ([]referrals.Prospect, []referrals.StageCount, error) {
	f.scope = s
	f.calls++
	return f.leads, []referrals.StageCount{}, f.failure
}
func (f *pipelineFake) SaveProspect(_ context.Context, s referrals.Scope, _, id string, in referrals.ProspectInput) (string, error) {
	f.scope = s
	f.calls++
	f.saved = in
	return id, f.failure
}
func (f *pipelineFake) DeleteProspect(_ context.Context, s referrals.Scope, _ string, _ int64) (bool, error) {
	f.scope = s
	f.calls++
	return true, f.failure
}
func (f *pipelineFake) Tasks(_ context.Context, s referrals.Scope, _ referrals.TaskFilter) ([]referrals.Task, referrals.TaskCounts, error) {
	f.scope = s
	f.calls++
	return f.tasks, referrals.TaskCounts{}, f.failure
}
func (f *pipelineFake) SaveTask(_ context.Context, s referrals.Scope, _, id string, in referrals.TaskInput) (string, error) {
	f.scope = s
	f.calls++
	f.taskInput = in
	return id, f.failure
}
func pipelineService() (*Service, *pipelineFake, state.Account) {
	s, base, acct := serviceFixture()
	f := &pipelineFake{fakeStore: base, team: []referrals.Rep{{ID: acct.ID, Email: acct.Email}, {ID: "22222222-2222-2222-2222-222222222222", Email: "other@example.com"}}}
	s.Config.Members["other@example.com"] = false
	s.Store = f
	return s, f, acct
}
func leadBody(owner string) string {
	return `{"name":"  Maya  ","email":"MAYA@EXAMPLE.COM","company":"Orbit","stage":"new","note":"Call","owner_id":"` + owner + `","version":0}`
}

func TestPipelineRequiresStaffVerificationAndMFA(t *testing.T) {
	for _, mutate := range []func(*Service, *state.Account){func(s *Service, _ *state.Account) { s.Config.Enabled = false }, func(_ *Service, a *state.Account) { a.Email = "outsider@example.com" }, func(_ *Service, a *state.Account) { a.EmailVerifiedAt = nil }, func(_ *Service, a *state.Account) { a.MFAEnrolledAt = nil }} {
		s, f, acct := pipelineService()
		mutate(s, &acct)
		handlers := []func(http.ResponseWriter, *http.Request, state.Account){s.Team, s.Leads, s.Tasks, s.SaveProspect, s.SaveTask, s.DeleteProspect}
		for _, handler := range handlers {
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest("POST", "/v1/sales/leads", strings.NewReader(leadBody(acct.ID))), acct)
			if w.Code != 403 && w.Code != 404 {
				t.Fatal(w.Code, w.Body.String())
			}
			if f.calls != 0 {
				t.Fatal("untrusted actor reached pipeline store")
			}
		}
	}
}

func TestProspectAssignmentAndStrictInput(t *testing.T) {
	s, f, acct := pipelineService()
	w := httptest.NewRecorder()
	s.SaveProspect(w, httptest.NewRequest("POST", "/v1/sales/leads", strings.NewReader(leadBody(f.team[1].ID))), acct)
	if w.Code != 403 || f.calls != 0 {
		t.Fatal("rep assigned another owner", w.Code, f.calls)
	}
	s.Config.Members[acct.Email] = true
	w = httptest.NewRecorder()
	s.SaveProspect(w, httptest.NewRequest("POST", "/v1/sales/leads", strings.NewReader(leadBody(f.team[1].ID))), acct)
	if w.Code != 200 || f.calls != 1 || !f.scope.All || f.saved.Name != "Maya" || f.saved.Email != "maya@example.com" {
		t.Fatal(w.Code, f.saved, f.scope)
	}
	for _, body := range []string{leadBody(acct.ID) + ` {}`, strings.Replace(leadBody(acct.ID), `"new"`, `"paid"`, 1), strings.Replace(leadBody(acct.ID), `"Call"`, `"`+strings.Repeat("界", 700)+`"`, 1), strings.Replace(leadBody(acct.ID), `"note":"Call"`, `"extra":true`, 1)} {
		before := f.calls
		w = httptest.NewRecorder()
		s.SaveProspect(w, httptest.NewRequest("POST", "/v1/sales/leads", strings.NewReader(body)), acct)
		if w.Code != 400 || f.calls != before {
			t.Fatal("invalid prospect reached store", w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	s.SaveProspect(w, httptest.NewRequest("POST", "/v1/sales/leads", strings.NewReader(leadBody(uuid.NewString()))), acct)
	if w.Code != 400 {
		t.Fatal("unconfigured owner accepted", w.Code)
	}
}

func TestPipelineErrorsAndTaskValidation(t *testing.T) {
	s, f, acct := pipelineService()
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{referrals.ErrConflict, 409, "sales_record_conflict"}, {referrals.ErrDuplicateLead, 409, "sales_lead_exists"}, {referrals.ErrNotFound, 404, "not_found"}} {
		f.failure = tc.err
		w := httptest.NewRecorder()
		s.SaveProspect(w, httptest.NewRequest("POST", "/v1/sales/leads", strings.NewReader(leadBody(acct.ID))), acct)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"title":"Call","due_on":"2026-02-30","lead_id":"` + acct.ID + `"}`, `{"title":"Call","due_on":"2101-01-01","lead_id":"` + acct.ID + `"}`, `{"title":"Call","due_on":"2026-11-03","lead_id":"` + acct.ID + `","done":true}`} {
		before := f.calls
		w := httptest.NewRecorder()
		s.SaveTask(w, httptest.NewRequest("POST", "/v1/sales/tasks", strings.NewReader(body)), acct)
		if w.Code != 400 || f.calls != before {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestPipelineCursorsAreBoundToResourceScopeAndFilters(t *testing.T) {
	s, f, acct := pipelineService()
	for range 26 {
		f.leads = append(f.leads, referrals.Prospect{ID: uuid.NewString()})
		f.tasks = append(f.tasks, referrals.Task{ID: uuid.NewString(), TaskInput: referrals.TaskInput{DueOn: "2026-11-03"}})
	}
	cursor := func(path string, handler func(http.ResponseWriter, *http.Request, state.Account)) string {
		t.Helper()
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", path, nil), acct)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var data struct {
			Next string `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || data.Next == "" {
			t.Fatal(err, w.Body.String())
		}
		return data.Next
	}
	leadCursor := cursor("/v1/sales/leads", s.Leads)
	taskCursor := cursor("/v1/sales/tasks?on=2026-11-03", s.Tasks)
	cases := []struct {
		path    string
		handler func(http.ResponseWriter, *http.Request, state.Account)
	}{{"/v1/sales/leads?stage=demo&after=" + leadCursor, s.Leads}, {"/v1/sales/leads?after=" + taskCursor, s.Leads}, {"/v1/sales/tasks?on=2026-11-04&after=" + taskCursor, s.Tasks}, {"/v1/sales/tasks?on=2026-11-03&after=" + leadCursor, s.Tasks}}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		tc.handler(w, httptest.NewRequest("GET", tc.path, nil), acct)
		if w.Code != 400 {
			t.Fatal("cursor escaped resource or filter scope", tc.path, w.Code)
		}
	}
	s.Config.Members[acct.Email] = true
	w := httptest.NewRecorder()
	s.Leads(w, httptest.NewRequest("GET", "/v1/sales/leads?after="+leadCursor, nil), acct)
	if w.Code != 400 {
		t.Fatal("rep cursor accepted with changed manager scope")
	}
}
