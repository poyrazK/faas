package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/middleware"
)

// issueDashboardFormToken mints an action-bound CSRF token for one form on
// a dashboard page and sets its named sidecar cookie. The account page
// renders several independent forms, so each action gets its own cookie
// name; the handler verifies with middleware.VerifyAuthenticatedNamed.
func (s *server) issueDashboardFormToken(w http.ResponseWriter, action, cookie, accountID string) (string, error) {
	tok, err := middleware.IssueForAuthenticatedNamed(s.sessions, action, accountID, cookie)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
	})
	return tok, nil
}
