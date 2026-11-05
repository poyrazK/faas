// A deleted app could not revoke its managed PostgreSQL credentials: the purge
// refuses a live binding (state.AppPurgeBlockedManagedPostgresBinding), so the
// credential stayed valid and the tombstone was never purged. Deleting the app
// is refused until its bindings are deleted.
package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestDeletingAnAppWithLiveDatabaseBindingsIsRefused(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "bound-app", 9004)
	_, _, databaseID := apidserver.ConfigureSourceRefManagedPostgres(t, env)
	srv := *env.Srv()
	acct, err := (*env.Store()).AccountByID(context.Background(), *env.AcctID())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(api.CreateManagedPostgresBindingRequest{AppID: *env.AppID(), Scope: "default", EnvironmentKey: "DATABASE_URL", Access: "read_write"})
	req := httptest.NewRequest(http.MethodPost, "/v1/postgres/databases/"+databaseID+"/bindings", bytes.NewReader(body))
	req.SetPathValue("id", databaseID)
	created := httptest.NewRecorder()
	srv.CreateManagedPostgresBinding(created, req, acct)
	var binding api.ManagedPostgresBinding
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &binding) != nil {
		t.Fatalf("create binding = %d %s", created.Code, created.Body)
	}
	call := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+*env.Key())
		w := httptest.NewRecorder()
		(*env.H()).ServeHTTP(w, r)
		return w
	}
	deleteApp := func() *httptest.ResponseRecorder { return call(http.MethodDelete, "/v1/apps/bound-app") }
	// data-api delete finds what to revoke by app, on any database.
	listed := call(http.MethodGet, "/v1/apps/bound-app/postgres-bindings")
	var bindings api.AppPostgresBindingList
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &bindings) != nil || len(bindings.Items) != 1 ||
		bindings.Items[0].ID != binding.ID || bindings.Items[0].EnvironmentKey != "DATABASE_URL" || bindings.Items[0].Database == "" {
		t.Fatalf("app bindings = %d %s", listed.Code, listed.Body)
	}
	refused := deleteApp()
	var problem api.Problem
	if refused.Code != http.StatusConflict || json.Unmarshal(refused.Body.Bytes(), &problem) != nil || problem.Code != "app_has_postgres_bindings" ||
		!strings.Contains(problem.Detail, binding.ID) || !strings.Contains(problem.Hint, "gregale data-api delete bound-app") {
		t.Fatalf("delete with a live binding = %d %s", refused.Code, refused.Body)
	}
	if _, err := (*srv.ManagedPostgresBindings()).Delete(context.Background(), acct.ID, binding.ID); err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	listed = call(http.MethodGet, "/v1/apps/bound-app/postgres-bindings")
	bindings = api.AppPostgresBindingList{}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &bindings) != nil {
		t.Fatalf("app bindings after revoking = %d %s", listed.Code, listed.Body)
	}
	for _, item := range bindings.Items {
		if item.State != "deleting" {
			t.Fatalf("a revoked binding is listed as %q: %s", item.State, listed.Body)
		}
	}
	if w := deleteApp(); w.Code >= 300 {
		t.Fatalf("delete after revoking the binding = %d %s", w.Code, w.Body)
	}
}
