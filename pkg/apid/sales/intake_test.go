package sales

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
)

type intakeFake struct {
	*fakeStore
	calls   int
	input   referrals.InquiryInput
	options referrals.IntakeOptions
}

func (f *intakeFake) SubmitInquiry(_ context.Context, _ string, _ time.Time, in referrals.InquiryInput, o referrals.IntakeOptions) error {
	f.calls++
	f.input = in
	f.options = o
	return nil
}
func (f *intakeFake) Inquiries(_ context.Context, scope referrals.Scope, _ referrals.InquiryFilter) ([]referrals.Inquiry, error) {
	f.scope = scope
	return []referrals.Inquiry{}, nil
}
func (f *intakeFake) AssignInquiry(context.Context, string, int64, string, referrals.IntakeOptions) error {
	f.calls++
	return nil
}
func (f *intakeFake) CloseInquiry(context.Context, referrals.Scope, string, int64) (bool, error) {
	f.calls++
	return true, nil
}
func TestSalesIntakeChallengeBoundaries(t *testing.T) {
	now := time.Now()
	key := []byte(strings.Repeat("k", 32))
	token := makeIntakeToken(key, now)
	for _, tc := range []struct {
		name  string
		key   []byte
		token string
		age   time.Duration
		valid bool
	}{{"premature", key, token, 0, false}, {"ready", key, token, api.SalesIntakeChallengeMinAge + time.Millisecond, true}, {"expired", key, token, api.SalesIntakeChallengeTTL + time.Second, false}, {"tampered", key, token + "x", time.Minute, false}, {"wrong key", []byte(strings.Repeat("q", 32)), token, time.Minute, false}, {"missing key", nil, token, time.Minute, false}} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseIntakeToken(tc.key, tc.token, now.Add(tc.age))
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}
func TestSalesIntakePublicValidationAndNoDisclosure(t *testing.T) {
	s, base, _ := serviceFixture()
	f := &intakeFake{fakeStore: base}
	s.Store = f
	s.Config.IntakeEnabled = true
	s.Config.IntakeFollowupDays = 2
	token := makeIntakeToken(s.intakeKey(), time.Now().Add(-time.Minute))
	body := map[string]any{"name": "  Maya  ", "email": "MAYA@EXAMPLE.COM", "company": "Orbit", "intent": "demo", "message": "Hello", "salesperson_code": "poju43", "challenge": token}
	request := func(body map[string]any) int {
		raw, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		s.SubmitInquiry(w, httptest.NewRequest("POST", "/v1/sales/inquiries", strings.NewReader(string(raw))))
		if w.Code == 202 && w.Body.String() != "{\"accepted\":true}\n" {
			t.Fatal("routing details disclosed", w.Body.String())
		}
		return w.Code
	}
	if code := request(body); code != 202 || f.calls != 1 || f.input.Name != "Maya" || f.input.Email != "maya@example.com" || f.input.Code != "POJU43" {
		t.Fatal(code, f)
	}
	body["website"] = "spam"
	if code := request(body); code != 202 || f.calls != 1 {
		t.Fatal(code, f.calls)
	}
	delete(body, "website")
	for _, tc := range []struct {
		key   string
		value any
	}{{"intent", "reward"}, {"salesperson_code", "BAD"}, {"message", strings.Repeat("界", 501)}, {"name", " "}, {"email", "User <a@example.com>"}, {"challenge", "invalid"}, {"extra", true}} {
		copy := map[string]any{}
		for k, v := range body {
			copy[k] = v
		}
		copy[tc.key] = tc.value
		if code := request(copy); code != 400 || f.calls != 1 {
			t.Fatal(tc.key, code, f.calls)
		}
	}
	s.Config.IntakeEnabled = false
	if code := request(body); code != 404 {
		t.Fatal(code)
	}
}
func TestSalesIntakeManagerAndStaffScopes(t *testing.T) {
	s, base, acct := serviceFixture()
	f := &intakeFake{fakeStore: base}
	s.Store = f
	r := httptest.NewRequest("PUT", "/v1/sales/inquiries/x/assignment", strings.NewReader(`{"owner_id":"`+acct.ID+`","version":1}`))
	r.SetPathValue("inquiry", acct.ID)
	w := httptest.NewRecorder()
	s.AssignInquiry(w, r, acct)
	if w.Code != 403 || f.calls != 0 {
		t.Fatal(w.Code, f.calls)
	}
	w = httptest.NewRecorder()
	s.Inquiries(w, httptest.NewRequest("GET", "/v1/sales/inquiries", nil), acct)
	if w.Code != 200 || f.scope.All || f.scope.OwnerID != acct.ID {
		t.Fatal(w.Code, f.scope)
	}
	acct.MFAEnrolledAt = nil
	w = httptest.NewRecorder()
	s.Inquiries(w, httptest.NewRequest("GET", "/v1/sales/inquiries", nil), acct)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestSalesIntakeConfiguration(t *testing.T) {
	env := map[string]string{"FAAS_SALES_ENABLED": "1", "FAAS_SALES_INTAKE_ENABLED": "1", "FAAS_SALES_INTAKE_FOLLOWUP_DAYS": "0", "FAAS_SALES_INTAKE_TIMEZONE": "Europe/Istanbul"}
	get := func(k string) string { return env[k] }
	c, err := FromEnv(get, "")
	if err != nil || !c.IntakeEnabled || c.IntakeFollowupDays != 0 || c.IntakeTimezone.String() != "Europe/Istanbul" {
		t.Fatal(c, err)
	}
	env["FAAS_SALES_INTAKE_FOLLOWUP_DAYS"] = "31"
	if _, err = FromEnv(get, ""); err == nil {
		t.Fatal("invalid deadline accepted")
	}
	env["FAAS_SALES_INTAKE_FOLLOWUP_DAYS"] = "1"
	env["FAAS_SALES_INTAKE_TIMEZONE"] = "invalid"
	if _, err = FromEnv(get, ""); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}
