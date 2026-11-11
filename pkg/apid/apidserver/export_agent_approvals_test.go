package apidserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/middleware"
)

// Human approval of agents' risky requests (ADR-20261104140058).

// RenderAgentApprovalForTestAs opens an approval request's review page as the
// signed-in account with email. It returns the response and, when the page
// offers the decision form, the CSRF cookie and form token.
func RenderAgentApprovalForTestAs(t *testing.T, srv http.Handler, id, email string) (rec *httptest.ResponseRecorder, cookieValue, formValue string) {
	t.Helper()
	h, ok := srv.(*cliAuthTestServer)
	if !ok {
		t.Fatal("RenderAgentApprovalForTestAs: unexpected handler type")
	}
	session := h.issueBrowserSessionForTest(t, email)
	rec = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/cli-auth?agent_approval="+url.QueryEscape(id), nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	srv.ServeHTTP(rec, r)
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.CookieNameAuthenticated {
			cookieValue = c.Value
		}
	}
	if m := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(rec.Body.String()); len(m) == 2 {
		formValue = m[1]
	}
	if cookieValue != "" {
		h.sessionCookies[cookieValue] = session
	}
	return rec, cookieValue, formValue
}

// DecideAgentApprovalForTest posts the review page's decision ("approve" or
// "deny") with the cookie and token RenderAgentApprovalForTestAs returned.
func DecideAgentApprovalForTest(t *testing.T, srv http.Handler, id, cookieValue, formValue, decision string) *httptest.ResponseRecorder {
	t.Helper()
	return DecideAgentApprovalWithNoteForTest(t, srv, id, cookieValue, formValue, decision, "")
}

// DecideAgentApprovalWithNoteForTest is DecideAgentApprovalForTest with the
// reason a person gives the agent (ADR-20261107140000 §6).
func DecideAgentApprovalWithNoteForTest(t *testing.T, srv http.Handler, id, cookieValue, formValue, decision, note string) *httptest.ResponseRecorder {
	t.Helper()
	h, ok := srv.(*cliAuthTestServer)
	if !ok {
		t.Fatal("DecideAgentApprovalForTest: unexpected handler type")
	}
	form := url.Values{"agent_approval": {id}, "decision": {decision}, "note": {note}, middleware.FormFieldName: {formValue}}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/cli-auth", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: h.sessionCookies[cookieValue]})
	r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: cookieValue})
	srv.ServeHTTP(rec, r)
	return rec
}
