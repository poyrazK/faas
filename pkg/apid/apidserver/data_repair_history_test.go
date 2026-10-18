package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestManagedDataRepairHistoryRequiresMFA(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, true)
	base := "/v1/apps/products/data-repair/sources/" + uuid.NewString()
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/v1/apps/products/data-repair/doctor?table=public.products"},
		{"GET", base + "/history"}, {"GET", base + "/plans/" + uuid.NewString()}, {"POST", base + "/plans/" + uuid.NewString() + "/verify"},
	} {
		r := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{"digest":"`+strings.Repeat("a", 64)+`"}`))
		r.AddCookie(e.MfaIssueWithPending(t, true))
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), api.CodeMFARequired) {
			t.Fatalf("history bypassed pending MFA: %d %s", w.Code, w.Body.String())
		}
	}
}
