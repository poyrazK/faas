package product

import (
	"net/http"
	"reflect"

	"github.com/onebox-faas/faas/pkg/state"
)

// Auth and Product Control have separate databases. Recheck Product state just
// before dispatch; a later concurrent publication cannot be serialized here.
func (s *Service) teamCurrentReview(r *http.Request, c customerTeam) error {
	fresh, err := s.customerRealm(r)
	if err != nil {
		return err
	}
	if fresh.account.ID != c.realm.account.ID || fresh.app.ID != c.realm.app.ID || fresh.authApp.ID != c.realm.authApp.ID || fresh.catalog.Revision != c.realm.catalog.Revision {
		return teamStale()
	}
	customer, err := fresh.store.ProductCustomerByAuthOrganization(r.Context(), fresh.account.ID, fresh.app.ID, c.identity.OrganizationID)
	if err != nil {
		return err
	}
	if customer.TenantID != c.identity.TenantID || customer.Suspended || !productTeamPermission(fresh.catalog, c.live.ActorRole) {
		return teamDenied()
	}
	if !reflect.DeepEqual(customer, c.customer) {
		return teamStale()
	}
	tenants, ok := s.Host.Store().(state.PlatformTenantStore)
	if !ok {
		return teamUnavailable()
	}
	tenant, err := tenants.GetPlatformTenant(r.Context(), fresh.account.ID, customer.TenantID)
	if err != nil {
		return err
	}
	if tenant.Status != state.PlatformTenantActive {
		return teamDenied()
	}
	return nil
}
