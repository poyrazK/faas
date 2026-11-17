package sales

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeStore struct {
	linkErr error
	rows    []referrals.Record
	scope   referrals.Scope
	link    referrals.Link
	writes  int
}

func (f *fakeStore) EnsureLink(_ context.Context, owner, kind string) (referrals.Link, error) {
	f.writes++
	f.link.OwnerID = owner
	f.link.Kind = kind
	return f.link, nil
}
func (f *fakeStore) Link(context.Context, string) (referrals.Link, error) { return f.link, f.linkErr }
func (f *fakeStore) OwnLink(context.Context, string, string) (referrals.Link, error) {
	return f.link, nil
}
func (f *fakeStore) SetLinkActive(_ context.Context, scope referrals.Scope, _ string, _ bool) (bool, error) {
	f.scope = scope
	f.writes++
	return true, nil
}
func (f *fakeStore) List(_ context.Context, scope referrals.Scope, _ string, _ int32) ([]referrals.Record, error) {
	f.scope = scope
	return append([]referrals.Record{}, f.rows...), nil
}
func (f *fakeStore) Summary(context.Context, referrals.Scope) ([]referrals.Summary, error) {
	return []referrals.Summary{{Kind: "customer", Currency: "usd", Signups: 1, PaidCents: 5000}}, nil
}
func (f *fakeStore) UpdateLead(_ context.Context, scope referrals.Scope, _ string, _ string, _ referrals.LeadUpdate) (bool, error) {
	f.scope = scope
	f.writes++
	return true, nil
}

func serviceFixture() (*Service, *fakeStore, state.Account) {
	now := time.Now()
	acct := state.Account{ID: "11111111-1111-1111-1111-111111111111", Email: "rep@example.com", EmailVerifiedAt: &now, MFAEnrolledAt: &now}
	store := &fakeStore{link: referrals.Link{Code: referrals.NewCode(), OwnerID: acct.ID, Kind: "customer", Active: true}}
	s := &Service{Store: store, Config: Config{Enabled: true, PublicOrigin: "https://api.gregale.dev", Members: map[string]bool{acct.Email: false}, Policy: referrals.Policy{Currency: "usd", HoldDays: 30, ConversionDays: 90}}, Key: func() []byte { return []byte(strings.Repeat("k", 32)) }, Account: func(context.Context, string) (state.Account, error) { return acct, nil }}
	return s, store, acct
}

func TestSalesScopesAndCustomerPrivacy(t *testing.T) {
	s, store, acct := serviceFixture()
	store.rows = []referrals.Record{{AccountID: "private-id", OwnerID: "private-owner", Email: "customer@example.com", Plan: "pro", AccountStatus: "active", Note: "private sales notes", FollowUp: "2026-12-01", Apps: 3, PaidCents: 5000, Currency: "usd", RewardCents: 1000}}
	r := httptest.NewRequest("GET", "/v1/sales/me", nil)
	w := httptest.NewRecorder()
	s.SalesData(w, r, acct)
	if w.Code != 200 || store.scope.All || store.scope.OwnerID != acct.ID {
		t.Fatal(w.Code, store.scope)
	}
	s.Config.Members[acct.Email] = true
	w = httptest.NewRecorder()
	s.SalesData(w, r, acct)
	if !store.scope.All {
		t.Fatal("manager lacks team scope")
	}
	w = httptest.NewRecorder()
	s.CustomerData(w, httptest.NewRequest("GET", "/v1/referrals/me", nil), acct)
	if w.Code != 200 || store.scope.All || store.scope.Kind != "customer" {
		t.Fatal(w.Code, store.scope)
	}
	for _, secret := range []string{"private-id", "private-owner", "customer@example.com", "private sales notes", "2026-12-01", "5000"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("customer received %q: %s", secret, w.Body.String())
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("customer data cacheable")
	}
}

func TestStaffRequiresMembershipVerificationAndMFA(t *testing.T) {
	s, store, acct := serviceFixture()
	for _, mutate := range []func(*state.Account){func(a *state.Account) { a.Email = "outsider@example.com" }, func(a *state.Account) { a.EmailVerifiedAt = nil }, func(a *state.Account) { a.MFAEnrolledAt = nil }} {
		a := acct
		mutate(&a)
		w := httptest.NewRecorder()
		s.SalesLink(w, httptest.NewRequest("POST", "/v1/sales/codes", nil), a)
		if w.Code != http.StatusForbidden || store.writes != 0 {
			t.Fatal(w.Code, store.writes)
		}
	}
}

func TestDisabledSurfacePrecedesAuthentication(t *testing.T) {
	s, _, _ := serviceFixture()
	s.Config.Enabled = false
	h := s.HideDisabled(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	for _, path := range []string{"/v1/sales/me", "/v1/sales/unknown", "/v1/referrals/codes", "/dashboard/sales", "/dashboard/referrals", "/dashboard/referral-assets/portal.js"} {
		for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != 404 {
				t.Fatalf("%s %s: %d", method, path, w.Code)
			}
		}
	}
}

func TestExplicitCodeSelectionAndRevokedRep(t *testing.T) {
	s, store, acct := serviceFixture()
	r := httptest.NewRequest("POST", "/v1/referrals/selection", strings.NewReader(`{"referral_code":"`+strings.ToLower(store.link.Code)+`"}`))
	w := httptest.NewRecorder()
	s.SelectCode(w, r)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.MaxAge != 900 {
		t.Fatal(cookie)
	}
	callback := httptest.NewRequest("GET", "/v1/auth/google/callback", nil)
	callback.AddCookie(cookie)
	if attr, err := s.SignupAttribution(callback); err != nil || attr == nil || attr.Code != store.link.Code {
		t.Fatal(attr, err)
	}
	store.link.Kind = "sales"
	delete(s.Config.Members, acct.Email)
	if attr, err := s.SignupAttribution(callback); err != nil || attr != nil {
		t.Fatal("removed sales member received attribution", attr, err)
	}
}

func TestSignupAcceptsTypedCodeInJSONAndForm(t *testing.T) {
	for _, tc := range []struct{ kind, body string }{{"application/json", `{"email":"new@example.com","password":"a-good-password","referral_code":" poju43 "}`}, {"application/x-www-form-urlencoded", "email=new%40example.com&password=a-good-password&referral_code=poju43"}} {
		t.Run(tc.kind, func(t *testing.T) {
			s, store, _ := serviceFixture()
			store.link.Code = "POJU43"
			r := httptest.NewRequest("POST", "/signup", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.kind)
			reached := false
			handler := s.WithSignupCode(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				attr, err := s.SignupAttribution(r)
				if err != nil || attr == nil || attr.Code != "POJU43" {
					t.Fatal(attr, err)
				}
				if tc.kind == "application/json" {
					var login struct {
						Email    string `json:"email"`
						Password string `json:"password"`
					}
					d := json.NewDecoder(r.Body)
					d.DisallowUnknownFields()
					if err := d.Decode(&login); err != nil || login.Email != "new@example.com" {
						t.Fatal(login, err)
					}
				}
			}))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if !reached {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestInvalidCodesNeverReachAccountCreation(t *testing.T) {
	for _, code := range []string{"BAD", "BAD!!!", "UNKNWN"} {
		s, store, _ := serviceFixture()
		store.linkErr = referrals.ErrNotFound
		r := httptest.NewRequest("POST", "/signup", strings.NewReader(`{"referral_code":"`+code+`"}`))
		r.Header.Set("Content-Type", "application/json")
		reached := false
		w := httptest.NewRecorder()
		s.WithSignupCode(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })).ServeHTTP(w, r)
		if reached || w.Code != 400 || !strings.Contains(w.Body.String(), "referral_code_invalid") {
			t.Fatal(code, w.Code, w.Body.String())
		}
	}
	s, store, _ := serviceFixture()
	store.link.Active = false
	r := httptest.NewRequest("POST", "/signup", strings.NewReader(`{"referral_code":"`+store.link.Code+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.WithSignupCode(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("paused code reached signup") })).ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestNativeSignupDoesNotUseAnEarlierOAuthSelection(t *testing.T) {
	for _, body := range []string{`{"referral_code":""}`, `{}`} {
		t.Run(body, func(t *testing.T) {
			s, store, _ := serviceFixture()
			r := httptest.NewRequest("POST", "/signup", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: touchCookie, Value: referrals.SignTouch(s.Key(), store.link.Code, time.Now())})
			w := httptest.NewRecorder()
			s.WithSignupCode(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attr, err := s.SignupAttribution(r)
				if attr != nil || err != nil {
					t.Fatal(attr, err)
				}
			})).ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestStrictLeadInput(t *testing.T) {
	for _, body := range []string{`{"stage":"new"} garbage`, `{"stage":"new"} {}`, `{"stage":"new","extra":true}`, `{"stage":"new"} null`} {
		w := httptest.NewRecorder()
		var update referrals.LeadUpdate
		if decode(w, httptest.NewRequest("PATCH", "/", strings.NewReader(body)), &update) {
			t.Fatalf("accepted %s", body)
		}
	}
	for _, update := range []referrals.LeadUpdate{{Stage: "won"}, {Stage: "new", Note: strings.Repeat("a", 2001)}, {Stage: "new", FollowUp: "2026-02-30"}} {
		if validLead(update) {
			t.Fatal("invalid lead accepted", update)
		}
	}
}

func TestConfigRejectsInvalidMoneyAndRanges(t *testing.T) {
	for key, value := range map[string]string{"FAAS_REFERRAL_REWARD_CENTS": "1.5", "FAAS_REFERRAL_NEW_CUSTOMER_REWARD_CENTS": "-1", "FAAS_SALES_COMMISSION_BPS": "10001", "FAAS_REFERRAL_CURRENCY": "gbp", "FAAS_REFERRAL_HOLD_DAYS": "366", "FAAS_REFERRAL_CONVERSION_DAYS": "0", "FAAS_SALES_EMAILS": "Someone <x@example.com>"} {
		t.Run(key, func(t *testing.T) {
			_, err := FromEnv(func(name string) string {
				if name == "FAAS_SALES_ENABLED" {
					return "1"
				}
				if name == key {
					return value
				}
				return ""
			}, "https://api.gregale.dev")
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestPortalRendersWithoutMockCustomerData(t *testing.T) {
	s, _, acct := serviceFixture()
	w := httptest.NewRecorder()
	s.CustomerPage(w, httptest.NewRequest("GET", "/dashboard/referrals", nil), acct)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Bring someone on board.") || strings.Contains(w.Body.String(), "customer@example.com") || strings.Contains(w.Body.String(), "Message templates") {
		t.Fatal(w.Code, w.Body.String())
	}
	var result map[string]any
	w = httptest.NewRecorder()
	s.CustomerData(w, httptest.NewRequest("GET", "/v1/referrals/me", nil), acct)
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result["payouts_enabled"] != false {
		t.Fatal(result, err)
	}
}

func TestCursorIsPrivateAndBoundToVisibility(t *testing.T) {
	s, _, _ := serviceFixture()
	const id = "11111111-1111-1111-1111-111111111111"
	token, err := s.encodeCursor(id, "customer:own")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(token, id) {
		t.Fatal("cursor discloses account ID")
	}
	got, err := s.decodeCursor(token, "customer:own")
	if err != nil || got != id {
		t.Fatal(got, err)
	}
	for _, audience := range []string{"other:own", "customer:team"} {
		if _, err := s.decodeCursor(token, audience); err == nil {
			t.Fatal("cursor crossed visibility boundary")
		}
	}
	if _, err := s.decodeCursor(token+"bad", "customer:own"); err == nil {
		t.Fatal("tampered cursor accepted")
	}
}

func TestSignupDoesNotSilentlyLoseAttributionOnReadFailure(t *testing.T) {
	s, store, _ := serviceFixture()
	r := httptest.NewRequest("POST", "/signup", nil)
	r.AddCookie(&http.Cookie{Name: touchCookie, Value: referrals.SignTouch(s.Key(), store.link.Code, time.Now())})
	store.linkErr = errors.New("temporary database failure")
	attr, err := s.SignupAttribution(r)
	if err == nil || attr != nil {
		t.Fatal("lookup failure was treated as an unattributed signup")
	}
}
