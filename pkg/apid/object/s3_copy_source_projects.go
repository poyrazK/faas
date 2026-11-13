package object

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// The route authorizes the destination app. A source selected by bucket ID
// must independently satisfy the caller's project restriction.
func (s *Service) authorizeCopySourceProject(w http.ResponseWriter, r *http.Request, acct state.Account, source state.ObjectBucket) bool {
	p, ok := httpkit.PrincipalFrom(r)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return false
	}
	if p.Key == nil || !p.Key.ProjectLimited {
		return true
	}
	app, err := s.Host.Store().AppByID(r.Context(), source.AppID)
	if err != nil {
		BucketProblem(w, err)
		return false
	}
	if app.AccountID != acct.ID || app.Status == state.AppDeleted || authmw.AppOutsideKeyProjects(p.Key, app) {
		BucketProblem(w, state.ErrNotFound)
		return false
	}
	return true
}
