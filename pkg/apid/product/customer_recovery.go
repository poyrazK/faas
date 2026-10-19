package product

import (
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/state"
)

// CustomerRecovery opens the live product realm's existing hosted recovery UI.
// Auth owns reset tokens, email delivery, password changes and session policy.
func (s *Service) CustomerRecovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.RawQuery != "" {
		customerError(w, state.ErrInvalidArgument)
		return
	}
	realm, err := s.customerRealm(r)
	if err != nil {
		customerError(w, err)
		return
	}
	target, err := customerRecoveryURL(realm)
	if err != nil {
		customerError(w, err)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func customerRecoveryURL(realm customerRealm) (string, error) {
	u, err := url.Parse(realm.origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || realm.authApp.ID == "" || realm.authApp.AccountID != realm.account.ID || realm.authApp.Slug != realm.catalog.Spec.AuthApp {
		return "", state.ErrProductAccess
	}
	u.Path = "/ui/forgot-password"
	// An explicit empty fragment prevents browsers from carrying a caller's
	// fragment across the redirect to the Auth app.
	return u.String() + "#", nil
}
