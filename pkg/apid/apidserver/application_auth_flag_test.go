// adr: 1190 — application Auth is a dark launch: until
// FAAS_APPLICATION_AUTH_ENABLED or FAAS_APPLICATION_AUTH_ACCOUNTS turns it
// on for an account, nothing of it reaches that account.
package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type authCapableProvider struct{ capabilityOnlyProvider }

func (authCapableProvider) Capabilities() managedpostgres.Capabilities {
	c := capabilityOnlyProvider{}.Capabilities()
	c.CredentialAccess = append(c.CredentialAccess, managedpostgres.CredentialAuth)
	return c
}

func TestApplicationAuthScreensAppearOnlyWhereItIsOn(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "")
	t.Setenv("FAAS_APPLICATION_AUTH_ACCOUNTS", "")
	e := apidserver.Setup(t, api.PlanPro)
	app, _ := apidserver.SeedAppTaskDeployment(t, e, "shop-auth")
	apidserver.AddAuthBindingForTest(t, e, app.ID)
	cookie := apidserver.RestartProgressDashboardCookie(t, e, (*e.Acct()).ID, false)
	link := `/dashboard/apps/` + app.Slug + `/auth"`
	if detail := apidserver.RestartProgressDashboardGET(e, cookie, "/dashboard/apps/"+app.Slug); detail.Code != http.StatusOK || strings.Contains(detail.Body.String(), link) {
		t.Fatalf("app page = %d, links Auth: %v", detail.Code, strings.Contains(detail.Body.String(), link))
	}
	if page := apidserver.RestartProgressDashboardGET(e, cookie, "/dashboard/apps/"+app.Slug+"/auth"); page.Code != http.StatusNotFound {
		t.Fatalf("Auth page = %d, want 404", page.Code)
	}
	t.Setenv("FAAS_APPLICATION_AUTH_ACCOUNTS", (*e.Acct()).ID)
	if detail := apidserver.RestartProgressDashboardGET(e, cookie, "/dashboard/apps/"+app.Slug); !strings.Contains(detail.Body.String(), link) {
		t.Fatal("the app page hides Auth from an account it is on for")
	}
	if page := apidserver.RestartProgressDashboardGET(e, cookie, "/dashboard/apps/"+app.Slug+"/auth"); page.Code != http.StatusOK {
		t.Fatalf("Auth page = %d with Auth on", page.Code)
	}
}

func TestApplicationAuthCredentialModeIsUnknownWhereItIsOff(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "")
	t.Setenv("FAAS_APPLICATION_AUTH_ACCOUNTS", "")
	e := apidserver.Setup(t, api.PlanHobby)
	(*(*e.S()).ManagedPostgres()) = capabilitiesServiceWith(t, (*e.Acct()).ID, authCapableProvider{})
	access := func() []string {
		response := e.Do(t, http.MethodGet, "/v1/postgres/capabilities", nil, nil)
		var out api.ManagedPostgresCapabilities
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || response.Code != http.StatusOK {
			t.Fatalf("capabilities: %d %s", response.Code, response.Body)
		}
		return out.CredentialAccess
	}
	if slices.Contains(access(), "auth") || !slices.Contains(access(), "read_only") {
		t.Fatalf("capabilities list auth while it is off: %v", access())
	}
	t.Setenv("FAAS_APPLICATION_AUTH_ACCOUNTS", (*e.Acct()).ID)
	if !slices.Contains(access(), "auth") {
		t.Fatalf("capabilities hide auth while it is on: %v", access())
	}

	t.Setenv("FAAS_APPLICATION_AUTH_ACCOUNTS", "")
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "auth-flag", 9001)
	_, _, databaseID := apidserver.ConfigureSourceRefManagedPostgres(t, env)
	acct, err := (*env.Store()).AccountByID(context.Background(), (*env.AcctID()))
	if err != nil {
		t.Fatal(err)
	}
	create := func(mode string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(api.CreateManagedPostgresBindingRequest{AppID: (*env.AppID()), Scope: "default", EnvironmentKey: "DATABASE_URL", Access: mode})
		req := httptest.NewRequest(http.MethodPost, "/v1/postgres/databases/"+databaseID+"/bindings", bytes.NewReader(body))
		req.SetPathValue("id", databaseID)
		response := httptest.NewRecorder()
		(*env.Srv()).CreateManagedPostgresBinding(response, req, acct)
		return response
	}
	auth, unknown := create("auth"), create("bogus")
	if auth.Code != unknown.Code || auth.Body.String() != unknown.Body.String() {
		t.Fatalf("auth while off = %d %s; an unknown mode = %d %s", auth.Code, auth.Body, unknown.Code, unknown.Body)
	}
}
