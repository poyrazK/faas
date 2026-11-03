package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// loadPreviewForStatus loads the preview a status read names. A credential
// bound to one app (a deploy token, or an OIDC bearer exchanged for that app)
// reads the status of that app's previews, so CI can wait for them without
// an account-wide key (ADR-20261102203228): it is authorized against the
// production app it is bound to. Every other principal loads the preview as
// any app.
func (s *server) loadPreviewForStatus(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	_, key, _, ok := authmw.PrincipalFrom(r)
	if !ok || key == nil || key.AppID == "" {
		return s.loadApp(w, r, acct, slug)
	}
	preview, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not load the preview"))
		return state.App{}, false
	}
	if err != nil || preview.PreviewOfSlug == "" || preview.Status == state.AppDeleted {
		s.notFound(w, "no such app")
		return state.App{}, false
	}
	parent, ok := s.loadApp(w, r, acct, preview.PreviewOfSlug)
	if !ok {
		return state.App{}, false
	}
	if parent.ID != key.AppID || parent.AccountID != preview.AccountID {
		s.notFound(w, "no such app")
		return state.App{}, false
	}
	return preview, true
}
