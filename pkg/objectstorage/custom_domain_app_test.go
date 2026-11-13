// adr: 167
package objectstorage

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type customDomainAppStore struct {
	exact    *state.CustomDomain
	wildcard state.CustomDomain
	plan     api.Plan
}

func (s *customDomainAppStore) DomainByName(context.Context, string) (state.CustomDomain, error) {
	if s.exact != nil {
		return *s.exact, nil
	}
	return state.CustomDomain{}, state.ErrNotFound
}
func (s *customDomainAppStore) WildcardDomainForHost(context.Context, string) (state.CustomDomain, error) {
	return s.wildcard, nil
}
func (*customDomainAppStore) AppByID(_ context.Context, id string) (state.App, error) {
	return state.App{ID: id, AccountID: "owner"}, nil
}
func (*customDomainAppStore) AppBySlug(context.Context, string) (state.App, error) {
	return state.App{}, state.ErrNotFound
}
func (*customDomainAppStore) ListObjectBuckets(context.Context, string, string) ([]state.ObjectBucket, error) {
	return nil, nil
}
func (s *customDomainAppStore) AccountByID(context.Context, string) (state.Account, error) {
	return state.Account{ID: "owner", Plan: s.plan}, nil
}
func (*customDomainAppStore) OrgByID(context.Context, string) (state.Org, error) {
	return state.Org{}, state.ErrNotFound
}

func TestStorageHandlersResolveWildcardDomains(t *testing.T) {
	now := time.Now()
	verified := state.CustomDomain{Domain: "*.example.com", AppID: "wildcard-app", VerifiedAt: now}
	exactVerified := state.CustomDomain{Domain: "customer.example.com", AppID: "exact-app", VerifiedAt: now}
	pending := state.CustomDomain{Domain: "customer.example.com", AppID: "exact-app", VerificationExpiresAt: now.Add(time.Hour)}
	expired := pending
	expired.VerificationExpiresAt = now.Add(-time.Hour)
	pendingWildcard := verified
	pendingWildcard.VerifiedAt = time.Time{}
	for _, tc := range []struct {
		name     string
		exact    *state.CustomDomain
		wildcard state.CustomDomain
		plan     api.Plan
		wantApp  string
	}{
		{"verified wildcard", nil, verified, api.PlanPro, "wildcard-app"},
		{"exact takes precedence", &exactVerified, verified, api.PlanPro, "exact-app"},
		{"live pending exact blocks wildcard", &pending, verified, api.PlanPro, ""},
		{"expired exact allows wildcard", &expired, verified, api.PlanPro, "wildcard-app"},
		{"pending wildcard blocks broader fallback", nil, pendingWildcard, api.PlanPro, ""},
		{"downgrade disables wildcard", nil, verified, api.PlanFree, ""},
		{"exact remains available on free plan", &exactVerified, verified, api.PlanFree, "exact-app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &customDomainAppStore{exact: tc.exact, wildcard: tc.wildcard, plan: tc.plan}
			reads := &publicReadHandler{store: store, appsDomain: "apps.gregale.dev"}
			app, found, err := reads.hostApp(t.Context(), "customer.example.com")
			if err != nil || found != (tc.wantApp != "") || app.ID != tc.wantApp {
				t.Fatalf("public reads: app=%q found=%v err=%v; want %q", app.ID, found, err, tc.wantApp)
			}
			uploads := &uploadHandler{store: store, appsDomain: "apps.gregale.dev"}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://customer.example.com/uploads/assets", nil)
			if err != nil {
				t.Fatal(err)
			}
			app, err = uploads.lookupApp(req)
			if app.ID != tc.wantApp || (err != nil) != (tc.wantApp == "") {
				t.Fatalf("uploads: app=%q err=%v; want %q", app.ID, err, tc.wantApp)
			}
		})
	}
}
