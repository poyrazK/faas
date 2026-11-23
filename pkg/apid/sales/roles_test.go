package sales

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

type teamFake struct {
	*fakeStore
	roster    map[string]string
	members   []referrals.TeamMember
	saved     []string
	moved     string
	payouts   int
	agreement referrals.AgreementInput
	payScope  referrals.Scope
	bps       int32
}

func (f *teamFake) TeamRoster(context.Context) (map[string]string, error) { return f.roster, nil }
func (f *teamFake) TeamMembers(context.Context, []string) ([]referrals.TeamMember, error) {
	if f.members != nil {
		return f.members, nil
	}
	return []referrals.TeamMember{{AccountID: "22222222-2222-2222-2222-222222222222", Email: "seller@example.com", StoredRole: referrals.RoleRep, StoredActive: true, Version: 1, AccountStatus: "active", EmailVerified: true, MFAEnrolled: true}}, nil
}
func (f *teamFake) AccountIDByEmail(context.Context, string) (string, error) {
	return "22222222-2222-2222-2222-222222222222", nil
}
func (f *teamFake) SaveTeamMember(_ context.Context, _, account, role string, _ bool, _ int64) error {
	f.saved = append(f.saved, account+":"+role)
	return nil
}
func (f *teamFake) AddAgreement(_ context.Context, _, _ string, in referrals.AgreementInput) (referrals.Agreement, error) {
	f.agreement = in
	return referrals.Agreement{CommissionBPS: in.CommissionBPS, EffectiveFrom: in.EffectiveFrom}, nil
}
func (f *teamFake) Agreements(context.Context, string) ([]referrals.Agreement, error) {
	return nil, nil
}
func (f *teamFake) CurrentCommission(_ context.Context, _ string, fallback int32) (int32, error) {
	if f.bps > 0 {
		return f.bps, nil
	}
	return fallback, nil
}
func (f *teamFake) MoveCredit(_ context.Context, _, customer, to, _ string, _ int32) (referrals.CreditChange, error) {
	f.moved = customer + "->" + to
	return referrals.CreditChange{ToOwnerID: to}, nil
}
func (f *teamFake) CreditHistory(context.Context, string) ([]referrals.CreditChange, error) {
	return nil, nil
}
func (f *teamFake) RecordPayout(_ context.Context, _, customer string, in referrals.PayoutInput) (referrals.Payout, error) {
	f.payouts++
	return referrals.Payout{AccountID: customer, AmountCents: in.AmountCents}, nil
}
func (f *teamFake) VoidPayout(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (f *teamFake) Payouts(_ context.Context, scope referrals.Scope, _ string, _ bool) ([]referrals.Payout, error) {
	f.payScope = scope
	return nil, nil
}

func teamFixture(role string) (*Service, *teamFake, state.Account) {
	s, base, acct := serviceFixture()
	s.Config.Members = map[string]bool{}
	s.Config.Admins = map[string]bool{}
	f := &teamFake{fakeStore: base, roster: map[string]string{}}
	s.Store = f
	s.Last = &LastRoster{}
	switch role {
	case referrals.RoleAdmin:
		s.Config.Admins[acct.Email] = true
		s.Config.Members[acct.Email] = true
	case "":
	default:
		f.roster[acct.Email] = role
	}
	return s, f, acct
}

func newRequest(method, path, body string, values map[string]string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range values {
		r.SetPathValue(k, v)
	}
	return r
}

func TestPortalMembersGetAccessAndRemovalTakesEffectNextRequest(t *testing.T) {
	s, f, acct := teamFixture(referrals.RoleRep)
	r := httptest.NewRequest("GET", "/v1/sales/me", nil)
	w := httptest.NewRecorder()
	s.SalesData(w, r, acct)
	if w.Code != 200 || f.scope.All {
		t.Fatal("portal rep", w.Code, f.scope)
	}
	if !s.CachedMember(acct.Email) {
		t.Fatal("roster read not cached for login checks")
	}
	f.roster[acct.Email] = referrals.RoleManager
	w = httptest.NewRecorder()
	s.SalesData(w, r, acct)
	if w.Code != 200 || !f.scope.All || !strings.Contains(w.Body.String(), `"role":"manager"`) {
		t.Fatal("portal manager", w.Code, f.scope, w.Body.String())
	}
	delete(f.roster, acct.Email)
	w = httptest.NewRecorder()
	s.SalesData(w, r, acct)
	if w.Code != 403 {
		t.Fatal("removed member kept access", w.Code)
	}
}

func TestPortalCannotGrantOwnerAdmin(t *testing.T) {
	s, f, acct := teamFixture(referrals.RoleAdmin)
	if role := mustRoster(t, s).role(acct.Email); role != referrals.RoleAdmin {
		t.Fatal(role)
	}
	f.roster["mallory@example.com"] = referrals.RoleAdmin
	if role := mustRoster(t, s).role("mallory@example.com"); role != "" {
		t.Fatal("stored admin role was honored", role)
	}
	// The strongest grant wins between config and portal.
	s.Config.Members["both@example.com"] = false
	f.roster["both@example.com"] = referrals.RoleManager
	if role := mustRoster(t, s).role("both@example.com"); role != referrals.RoleManager {
		t.Fatal(role)
	}
}

func mustRoster(t *testing.T, s *Service) roster {
	t.Helper()
	r, err := s.roster(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestManagersManageTheTeam(t *testing.T) {
	member := "22222222-2222-2222-2222-222222222222"
	for _, role := range []string{referrals.RoleRep, referrals.RoleManager, referrals.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			s, f, acct := teamFixture(role)
			w := httptest.NewRecorder()
			s.AdminTeam(w, newRequest("GET", "/v1/sales/admin/team", "", nil), acct)
			list := w.Code
			w = httptest.NewRecorder()
			s.AddMember(w, newRequest("POST", "/v1/sales/admin/team", `{"email":"seller@example.com","role":"rep"}`, nil), acct)
			add := w.Code
			w = httptest.NewRecorder()
			s.UpdateMember(w, newRequest("PUT", "/v1/sales/admin/team/"+member, `{"role":"rep","active":false,"version":1}`, map[string]string{"member": member}), acct)
			remove := w.Code
			if role == referrals.RoleRep {
				if list != 403 || add != 403 || remove != 403 || len(f.saved) != 0 {
					t.Fatal("rep managed the team", list, add, remove, f.saved)
				}
				return
			}
			if list != 200 || add != 200 || remove != 200 || len(f.saved) != 2 {
				t.Fatal(role, list, add, remove, f.saved)
			}
		})
	}
}

// Managers manage salespeople only; managers stay with owner admins.
func TestManagersCannotManageManagers(t *testing.T) {
	member := "22222222-2222-2222-2222-222222222222"
	manager := []referrals.TeamMember{{AccountID: member, Email: "seller@example.com", StoredRole: referrals.RoleManager, StoredActive: true, Version: 1}}
	configManager := []referrals.TeamMember{{AccountID: member, Email: "boss@example.com", StoredRole: referrals.RoleRep, StoredActive: true, Version: 1}}
	update := func(body string) *http.Request {
		return newRequest("PUT", "/v1/sales/admin/team/"+member, body, map[string]string{"member": member})
	}
	for _, tt := range []struct {
		name    string
		members []referrals.TeamMember
		run     func(*Service, *httptest.ResponseRecorder, state.Account)
	}{
		{"add a manager", nil, func(s *Service, w *httptest.ResponseRecorder, a state.Account) {
			s.AddMember(w, newRequest("POST", "/v1/sales/admin/team", `{"email":"alt@example.com","role":"manager"}`, nil), a)
		}},
		{"promote a salesperson", nil, func(s *Service, w *httptest.ResponseRecorder, a state.Account) {
			s.UpdateMember(w, update(`{"role":"manager","active":true,"version":1}`), a)
		}},
		{"deactivate a manager", manager, func(s *Service, w *httptest.ResponseRecorder, a state.Account) {
			s.UpdateMember(w, update(`{"role":"rep","active":false,"version":1}`), a)
		}},
		{"demote a manager", manager, func(s *Service, w *httptest.ResponseRecorder, a state.Account) {
			s.UpdateMember(w, update(`{"role":"rep","active":true,"version":1}`), a)
		}},
		{"touch a config manager's rep row", configManager, func(s *Service, w *httptest.ResponseRecorder, a state.Account) {
			s.UpdateMember(w, update(`{"role":"rep","active":false,"version":1}`), a)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, f, acct := teamFixture(referrals.RoleManager)
			f.members = tt.members
			s.Config.Members["boss@example.com"] = true // config manager
			w := httptest.NewRecorder()
			tt.run(s, w, acct)
			if w.Code != 403 || len(f.saved) != 0 {
				t.Fatal(w.Code, f.saved, w.Body.String())
			}
			// An owner admin may do the same.
			s, f, acct = teamFixture(referrals.RoleAdmin)
			f.members = tt.members
			w = httptest.NewRecorder()
			tt.run(s, w, acct)
			if w.Code != 200 || len(f.saved) != 1 {
				t.Fatal("admin", w.Code, f.saved, w.Body.String())
			}
		})
	}
}

func TestManagersDoNotSeeCommissionTerms(t *testing.T) {
	bps := int32(1500)
	for _, role := range []string{referrals.RoleManager, referrals.RoleAdmin} {
		s, f, acct := teamFixture(role)
		f.members = []referrals.TeamMember{{AccountID: "22222222-2222-2222-2222-222222222222", Email: "seller@example.com", CommissionBPS: &bps}}
		w := httptest.NewRecorder()
		s.AdminTeam(w, newRequest("GET", "/v1/sales/admin/team", "", nil), acct)
		body := w.Body.String()
		terms := strings.Contains(body, `"commission_bps":1500`) || strings.Contains(body, "default_commission_bps")
		if w.Code != 200 || terms != (role == referrals.RoleAdmin) {
			t.Fatal(role, w.Code, body)
		}
	}
}

func TestManagersCannotChangeThemselves(t *testing.T) {
	for _, body := range []string{`{"role":"manager","active":false,"version":1}`, `{"role":"rep","active":true,"version":1}`} {
		s, f, acct := teamFixture(referrals.RoleManager)
		w := httptest.NewRecorder()
		s.UpdateMember(w, newRequest("PUT", "/v1/sales/admin/team/"+acct.ID, body, map[string]string{"member": acct.ID}), acct)
		if w.Code != 400 || len(f.saved) != 0 {
			t.Fatal(body, w.Code, f.saved, w.Body.String())
		}
	}
}

func TestOwnerAdminOnlyActions(t *testing.T) {
	type action struct {
		name, method, path, body string
		pathValues               map[string]string
		run                      func(*Service, *httptest.ResponseRecorder, *http.Request, state.Account)
	}
	customer := "33333333-3333-3333-3333-333333333333"
	member := "22222222-2222-2222-2222-222222222222"
	actions := []action{
		{"agreement", "POST", "/v1/sales/admin/team/" + member + "/agreements", `{"commission_bps":1500,"note":"2026 contract"}`, map[string]string{"member": member}, func(s *Service, w *httptest.ResponseRecorder, r *http.Request, a state.Account) {
			s.AddAgreement(w, r, a)
		}},
		{"move credit", "PUT", "/v1/sales/customers/" + customer + "/credit", `{"owner_id":"` + member + `","reason":"wrong code"}`, map[string]string{"account": customer}, func(s *Service, w *httptest.ResponseRecorder, r *http.Request, a state.Account) {
			s.MoveCredit(w, r, a)
		}},
		{"payout", "POST", "/v1/sales/customers/" + customer + "/payouts", `{"amount_cents":500,"paid_on":"2026-11-20","reference":"wire"}`, map[string]string{"account": customer}, func(s *Service, w *httptest.ResponseRecorder, r *http.Request, a state.Account) {
			s.RecordPayout(w, r, a)
		}},
	}
	for _, role := range []string{referrals.RoleRep, referrals.RoleManager, referrals.RoleAdmin} {
		for _, a := range actions {
			t.Run(role+"/"+a.name, func(t *testing.T) {
				s, f, acct := teamFixture(role)
				f.roster["seller@example.com"] = referrals.RoleRep
				now := time.Now()
				target := state.Account{ID: member, Email: "seller@example.com", Status: state.AccountActive, EmailVerifiedAt: &now, MFAEnrolledAt: &now}
				s.Account = func(context.Context, string) (state.Account, error) { return target, nil }
				r := newRequest(a.method, a.path, a.body, a.pathValues)
				w := httptest.NewRecorder()
				a.run(s, w, r, acct)
				if role != referrals.RoleAdmin {
					if w.Code != 403 {
						t.Fatalf("%s reached %s: %d %s", role, a.name, w.Code, w.Body.String())
					}
					return
				}
				if w.Code != 200 {
					t.Fatalf("admin %s: %d %s", a.name, w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestAgreementDatesAndRates(t *testing.T) {
	member := "22222222-2222-2222-2222-222222222222"
	for _, tt := range []struct {
		name, body string
		want       int
	}{
		{"today", `{"commission_bps":1500}`, 200},
		{"future", `{"commission_bps":1500,"effective_from":"` + time.Now().UTC().AddDate(0, 1, 0).Format(time.DateOnly) + `"}`, 200},
		{"past", `{"commission_bps":1500,"effective_from":"2020-01-01"}`, 400},
		{"too high", `{"commission_bps":10001}`, 400},
		{"missing", `{"note":"x"}`, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, f, acct := teamFixture(referrals.RoleAdmin)
			f.roster["seller@example.com"] = referrals.RoleRep
			now := time.Now()
			s.Account = func(context.Context, string) (state.Account, error) {
				return state.Account{ID: member, Email: "seller@example.com", EmailVerifiedAt: &now, MFAEnrolledAt: &now}, nil
			}
			r := newRequest("POST", "/v1/sales/admin/team/"+member+"/agreements", tt.body, map[string]string{"member": member})
			w := httptest.NewRecorder()
			s.AddAgreement(w, r, acct)
			if w.Code != tt.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestAgreementRequiresCurrentMemberAndCreditTargetMustSignIn(t *testing.T) {
	member := "22222222-2222-2222-2222-222222222222"
	s, f, acct := teamFixture(referrals.RoleAdmin)
	s.Account = func(context.Context, string) (state.Account, error) {
		return state.Account{ID: member, Email: "former@example.com"}, nil
	}
	r := newRequest("POST", "/x", `{"commission_bps":100}`, map[string]string{"member": member})
	w := httptest.NewRecorder()
	s.AddAgreement(w, r, acct)
	if w.Code != 404 {
		t.Fatal("agreement for non-member", w.Code)
	}
	// On the team but without an authenticator: credit cannot move there.
	f.roster["former@example.com"] = referrals.RoleRep
	r = newRequest("PUT", "/x", `{"owner_id":"`+member+`","reason":"r"}`, map[string]string{"account": "33333333-3333-3333-3333-333333333333"})
	w = httptest.NewRecorder()
	s.MoveCredit(w, r, acct)
	if w.Code != 404 || f.moved != "" {
		t.Fatal("credit moved to member without MFA", w.Code, f.moved)
	}
}

func TestSalespeopleSeeTheirOwnAgreementAndPayouts(t *testing.T) {
	s, f, acct := teamFixture(referrals.RoleRep)
	f.bps = 2200
	s.Config.Policy.CommissionBPS = 1000
	w := httptest.NewRecorder()
	s.SalesData(w, httptest.NewRequest("GET", "/v1/sales/me", nil), acct)
	var body struct {
		Policy referrals.Policy `json:"policy"`
		Role   string           `json:"role"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Policy.CommissionBPS != 2200 || body.Role != referrals.RoleRep {
		t.Fatal(w.Body.String(), err)
	}
	w = httptest.NewRecorder()
	s.Payouts(w, httptest.NewRequest("GET", "/v1/sales/payouts", nil), acct)
	if w.Code != 200 || f.payScope.All || f.payScope.OwnerID != acct.ID {
		t.Fatal(w.Code, f.payScope)
	}
	w = httptest.NewRecorder()
	s.Payouts(w, httptest.NewRequest("GET", "/v1/sales/payouts?owner=22222222-2222-2222-2222-222222222222", nil), acct)
	if w.Code != 400 {
		t.Fatal("rep read another owner's payouts", w.Code)
	}
}

func TestCustomersNeverSeeSalesTerms(t *testing.T) {
	s, f, acct := teamFixture("")
	f.rows = []referrals.Record{{Kind: "customer", OrgName: "Acme", CommissionBPS: 1500, PayoutCents: 10, PayoutPaidOn: "2026-11-20"}}
	w := httptest.NewRecorder()
	s.CustomerData(w, httptest.NewRequest("GET", "/v1/referrals/me", nil), acct)
	for _, leak := range []string{"Acme", "commission_bps\":1500", "payout_cents", "payout_paid_on"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatal("customer response leaked", leak, w.Body.String())
		}
	}
}
