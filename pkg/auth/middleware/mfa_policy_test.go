package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomerStepUpUsesActorsEnabledFactors(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		actorMFA, ownerMFA, strict bool
		want                       int
	}{
		{"optional customer", false, false, false, 200},
		{"enrolled actor", true, false, false, 403},
		{"owner cannot require actor enrollment", false, true, false, 200},
		{"strict operations still need proof", false, false, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mw := newMW(t, newFakeAuthn(), nil, nil, &fakeAuditor{})
			actor := state.Account{ID: "actor", MFARequired: tc.actorMFA}
			owner := state.Account{ID: "owner", MFARequired: tc.ownerMFA}
			r := httptest.NewRequest("POST", "/v1/keys", nil)
			r = r.WithContext(authmw.WithPrincipal(r.Context(), actor, nil, nil))
			r = r.WithContext(authmw.WithStepUp(r.Context(), time.Time{}))
			rec := httptest.NewRecorder()
			next := func(w http.ResponseWriter, _ *http.Request, subject state.Account) {
				if subject.ID != owner.ID {
					t.Error("step-up replaced the resource owner")
				}
				w.WriteHeader(200)
			}
			gate := mw.RequireStepUp(5 * time.Minute)
			if tc.strict {
				gate = mw.RequireStepUpStrict(5 * time.Minute)
			}
			gate(next)(rec, r, owner)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d", rec.Code, tc.want)
			}
		})
	}
}

func TestCustomerDashboardStepUpHonorsOptionalMFA(t *testing.T) {
	mw := newMW(t, newFakeAuthn(), nil, nil, &fakeAuditor{})
	for _, required := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/dashboard/upgrade", nil)
		r = r.WithContext(authmw.WithPrincipal(r.Context(), state.Account{ID: "customer", MFARequired: required}, nil, nil))
		r = r.WithContext(authmw.WithStepUp(r.Context(), time.Time{}))
		rec := httptest.NewRecorder()
		mw.RequireStepUpHandler(5*time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(rec, r)
		want := 204
		if required {
			want = 403
		}
		if rec.Code != want {
			t.Fatalf("MFA=%v status=%d want=%d", required, rec.Code, want)
		}
	}
}

func TestDeploymentMandatoryMFACannotUseOptionalCustomerBypass(t *testing.T) {
	mw := newMW(t, newFakeAuthn(), nil, nil, &fakeAuditor{})
	mw.MandatoryMFA = func(state.Account) bool { return true }
	acct := state.Account{ID: "operator"}
	r := httptest.NewRequest("POST", "/v1/keys", nil)
	r = r.WithContext(authmw.WithStepUp(r.Context(), time.Time{}))
	rec := httptest.NewRecorder()
	mw.RequireStepUp(5*time.Minute)(func(http.ResponseWriter, *http.Request, state.Account) {
		t.Error("operator without MFA proof reached the handler")
	})(rec, r, acct)
	if rec.Code != 403 {
		t.Fatalf("operator status=%d", rec.Code)
	}
}
