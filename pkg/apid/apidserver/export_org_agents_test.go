package apidserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/middleware"
)

// Hosted MCP sign-ins that act in an organization (ADR-20261101225122).
var (
	NewCliAuthTestServer   = newCliAuthTestServer
	RenderCliAuthForTestAs = renderCliAuthForTestAs
)

// PostCliAuthWithOrgForTest approves a code on the approval page as
// postCliAuthForTest does, choosing the organization the connection acts in.
func PostCliAuthWithOrgForTest(t *testing.T, srv http.Handler, code, email, cookieValue, formValue, org string) *httptest.ResponseRecorder {
	t.Helper()
	h, ok := srv.(*cliAuthTestServer)
	if !ok {
		t.Fatal("PostCliAuthWithOrgForTest: unexpected handler type")
	}
	form := url.Values{"code": {strings.ReplaceAll(code, "-", "")}, "email": {email}, middleware.FormFieldName: {formValue}, "org": {org}}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/cli-auth", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: h.sessionCookies[cookieValue]})
	r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: cookieValue})
	srv.ServeHTTP(rec, r)
	return rec
}
