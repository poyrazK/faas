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
	deleteApp := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/v1/apps/bound-app", nil)
		r.Header.Set("Authorization", "Bearer "+*env.Key())
		w := httptest.NewRecorder()
		(*env.H()).ServeHTTP(w, r)
		return w
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
	if w := deleteApp(); w.Code >= 300 {
		t.Fatalf("delete after revoking the binding = %d %s", w.Code, w.Body)
	}
}
