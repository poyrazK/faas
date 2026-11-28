package apidserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Organization seat tests (ADR-20261127200000).
var (
	SeedSharedOrgWithOutsideOwner      = seedSharedOrgWithOutsideOwner
	SeedInvitationForPrincipal         = seedInvitationForPrincipal
	SetupWithSessionFreshStepUpForTest = setupWithSessionFreshStepUpForTest
)

// DoWithCookie sends a request authenticated by a browser session cookie.
func (r testEnv) DoWithCookie(t *testing.T, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return r.doWithCookie(t, method, path, cookie)
}
