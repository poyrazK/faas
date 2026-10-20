// adr: 521
package operation

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// activePlatformTenantSelfID returns the token's customer only while that
// customer is active. Suspension closes the customer's Operation reads,
// events and downloads at once; the account keeps every outcome. Customer
// writes (cancel, approve, signal) keep their own rules.
func (s *Service) activePlatformTenantSelfID(w http.ResponseWriter, r *http.Request, acct state.Account) (string, bool) {
	tenant, ok := httpkit.PlatformTenantSelfID(w, r)
	if !ok {
		return "", false
	}
	tenants, ok := s.Host.Store().(state.PlatformTenantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("platform tenant store unavailable"))
		return "", false
	}
	customer, err := tenants.GetPlatformTenant(r.Context(), acct.ID, tenant)
	if err != nil {
		WriteOperationError(w, err)
		return "", false
	}
	if customer.Status != state.PlatformTenantActive {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Customer suspended",
			"the owning account suspended this customer; its operations are readable again once it is resumed"))
		return "", false
	}
	return tenant, true
}

// readablePlatformTenantSelfOperation is platformTenantSelfOperation for the
// read routes: the customer must still be active.
func (s *Service) readablePlatformTenantSelfOperation(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Operation, state.OperationStore, bool) {
	if _, ok := s.activePlatformTenantSelfID(w, r, acct); !ok {
		return state.Operation{}, nil, false
	}
	return s.platformTenantSelfOperation(w, r, acct)
}
