// adr: 570
package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) deleteDomainIntent(r *http.Request, account state.Account, app state.App, domain string, activity state.OrgActivity) (int64, error) {
	if owned, ok := s.store.(state.CustomDomainRemovalOwnerStore); ok {
		prepared, prepareErr := s.prepareAppActivity(r.Context(), r, account, app, activity)
		if prepareErr == nil {
			return owned.DeleteCustomDomainForAppWithActivity(r.Context(), domain, app.ID, prepared)
		}
		return 0, owned.DeleteCustomDomainForApp(r.Context(), domain, app.ID)
	}
	if _, authoritative := s.store.(state.PublicHostPolicySnapshotStore); authoritative {
		return 0, errors.New("owner-bound domain removal is unavailable")
	}
	if mutation, ok := s.store.(state.OrgActivityDomainMutationStore); ok {
		prepared, prepareErr := s.prepareAppActivity(r.Context(), r, account, app, activity)
		if prepareErr == nil {
			return mutation.DeleteCustomDomainWithActivity(r.Context(), domain, prepared)
		}
	}
	return 0, s.store.DeleteCustomDomain(r.Context(), domain)
}
