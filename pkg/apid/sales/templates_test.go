package sales

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

type templateFake struct {
	*fakeStore
	calls  int
	input  referrals.MessageTemplateInput
	filter referrals.TemplateFilter
	source referrals.TemplateSource
	rows   []referrals.MessageTemplate
	err    error
}

func (f *templateFake) MessageTemplate(_ context.Context, s referrals.Scope, id string) (referrals.MessageTemplate, error) {
	f.calls++
	f.scope = s
	return f.source.Template, f.err
}
func (f *templateFake) MessageTemplates(_ context.Context, s referrals.Scope, filter referrals.TemplateFilter) ([]referrals.MessageTemplate, error) {
	f.calls++
	f.scope = s
	f.filter = filter
	return f.rows, f.err
}
func (f *templateFake) SaveMessageTemplate(_ context.Context, s referrals.Scope, _ string, in referrals.MessageTemplateInput) (string, error) {
	f.calls++
	f.scope = s
	f.input = in
	return uuid.NewString(), f.err
}
func (f *templateFake) MessageTemplateSource(_ context.Context, s referrals.Scope, _, _ string, _ int64) (referrals.TemplateSource, error) {
	f.calls++
	f.scope = s
	return f.source, f.err
}
func templateService() (*Service, *templateFake, state.Account) {
	s, b, a := serviceFixture()
	f := &templateFake{fakeStore: b}
	s.Store = f
	return s, f, a
}
func TestTemplateAccessAndManagerWrites(t *testing.T) {
	for _, change := range []func(*Service, *state.Account){func(s *Service, _ *state.Account) { s.Config.Enabled = false }, func(_ *Service, a *state.Account) { a.Email = "outside@example.com" }, func(_ *Service, a *state.Account) { a.EmailVerifiedAt = nil }, func(_ *Service, a *state.Account) { a.MFAEnrolledAt = nil }} {
		s, f, a := templateService()
		change(s, &a)
		for _, h := range []func(http.ResponseWriter, *http.Request, state.Account){s.Templates, s.Template, s.SaveTemplate, s.PreviewTemplate} {
			w := httptest.NewRecorder()
			h(w, httptest.NewRequest("GET", "/", nil), a)
			if (w.Code != 403 && w.Code != 404) || f.calls != 0 {
				t.Fatal(w.Code, f.calls)
			}
		}
	}
	s, f, a := templateService()
	w := httptest.NewRecorder()
	s.SaveTemplate(w, httptest.NewRequest("POST", "/", strings.NewReader(`{}`)), a)
	if w.Code != 403 || f.calls != 0 {
		t.Fatal(w.Code, f.calls)
	}
}
func TestTemplateInputValidationAndConflict(t *testing.T) {
	s, f, a := templateService()
	s.Config.Members[a.Email] = true
	base := referrals.MessageTemplateInput{Title: " Intro ", Category: "intro", Status: "draft", Subject: " Hi ", Body: " Hello {{lead_name}} "}
	send := func(in referrals.MessageTemplateInput, id string) int {
		raw, _ := json.Marshal(in)
		r := httptest.NewRequest("POST", "/", strings.NewReader(string(raw)))
		r.SetPathValue("template", id)
		w := httptest.NewRecorder()
		s.SaveTemplate(w, r, a)
		return w.Code
	}
	if code := send(base, ""); code != 200 || f.input.Title != "Intro" || f.input.Body != "Hello {{lead_name}}" || !f.scope.All {
		t.Fatal(code, f.input)
	}
	in := base
	in.Body = "{{price}}"
	before := f.calls
	if code := send(in, ""); code != 400 || f.calls != before {
		t.Fatal(code, f.calls)
	}
	if code := send(base, a.ID); code != 400 || f.calls != before {
		t.Fatal("missing version", code, f.calls)
	}
	base.Version = 1
	f.err = referrals.ErrConflict
	if code := send(base, a.ID); code != 409 {
		t.Fatal(code)
	}
	// Strict JSON rejects unknown fields and trailing objects.
	for _, body := range []string{`{"send":true}`, `{} {}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.SaveTemplate(w, r, a)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
func TestTemplatePaginationRoleBoundCursors(t *testing.T) {
	s, f, a := templateService()
	s.Config.Members[a.Email] = true
	for range api.SalesPageSize + 1 {
		f.rows = append(f.rows, referrals.MessageTemplate{ID: uuid.NewString()})
	}
	w := httptest.NewRecorder()
	s.Templates(w, httptest.NewRequest("GET", "/", nil), a)
	var first struct {
		Templates []referrals.MessageTemplate `json:"templates"`
		Next      string                      `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(first.Templates) != 25 || first.Next == "" || f.filter.Limit != 26 {
		t.Fatal(w.Code, first)
	}
	next := httptest.NewRequest("GET", "/?after="+first.Next, nil)
	w = httptest.NewRecorder()
	s.Templates(w, next, a)
	if w.Code != 200 || f.filter.After != first.Templates[24].ID {
		t.Fatal(w.Code, f.filter)
	}
	s.Config.Members[a.Email] = false
	before := f.calls
	w = httptest.NewRecorder()
	s.Templates(w, next, a)
	if w.Code != 400 || f.calls != before {
		t.Fatal("role cursor reused", w.Code)
	}
}
func TestTemplatePreviewCodeEligibilityAndAuditPrivacy(t *testing.T) {
	s, f, a := templateService()
	f.source = referrals.TemplateSource{Template: referrals.MessageTemplate{MessageTemplateInput: referrals.MessageTemplateInput{Body: "Hi {{lead_name}}: {{referral_code}}", Version: 2}}, Lead: referrals.TemplateLead{Name: "Maya", OwnerEmail: "removed@example.com", ReferralCode: "POJU43"}}
	audit := map[string]any{}
	s.Audit = func(_ context.Context, _ string, _ *string, data map[string]any) { audit = data }
	r := httptest.NewRequest("GET", "/?version=2", nil)
	r.SetPathValue("lead", a.ID)
	r.SetPathValue("template", a.ID)
	w := httptest.NewRecorder()
	s.PreviewTemplate(w, r, a)
	var data struct {
		Message referrals.MessagePreview `json:"message"`
		Lead    referrals.TemplateLead   `json:"lead"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(data.Message.Missing) != 1 || data.Lead.ReferralCode != "" || strings.Contains(data.Message.Body, "POJU43") {
		t.Fatal(w.Code, data)
	}
	for key := range audit {
		if key != "lead_id" && key != "template_id" && key != "template_version" {
			t.Fatal("content in audit", audit)
		}
	}
	f.source.Lead.OwnerEmail = a.Email
	w = httptest.NewRecorder()
	s.PreviewTemplate(w, r, a)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "POJU43") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		err  error
		code int
	}{{referrals.ErrConflict, 409}, {referrals.ErrNotFound, 404}, {errors.New("db"), 503}} {
		f.err = tc.err
		w = httptest.NewRecorder()
		s.PreviewTemplate(w, r, a)
		if w.Code != tc.code {
			t.Fatal(w.Code, tc.code)
		}
	}
}
