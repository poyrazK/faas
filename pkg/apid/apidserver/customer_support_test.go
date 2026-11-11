// adr: 20261111130000
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

func TestCustomerSupportAuthenticatedRoutes(t *testing.T) {
	pool := pgtest.Open(t)
	ddl, e := os.ReadFile("../support/schema.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(t.Context(), string(ddl)); e != nil {
		t.Fatal(e)
	}
	env := apidserver.Setup(t, api.PlanPro)
	acct := *env.Acct()
	if err := (*env.S()).InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	if got := env.Do(t, http.MethodGet, "/v1/support/tickets", nil, nil); got.Code != 404 {
		t.Fatalf("preview exposed by default: %d %s", got.Code, got.Body)
	}
	flag, ok := platformflags.CapabilityFlag("customer-support")
	if !ok {
		t.Fatal("missing support capability flag")
	}
	allow := true
	(*env.S()).SeedPlatformFlag(flag, platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{{ID: "support-preview", Accounts: []string{acct.ID}, Value: &allow}}})

	store := *env.Store()
	if _, e = pool.Exec(t.Context(), `INSERT INTO accounts VALUES($1,$2,'pro','active',now(),now())`, acct.ID, acct.Email); e != nil {
		t.Fatal(e)
	}
	key, e := store.APIKeyByHash(t.Context(), api.HashToken([]byte(*env.Key())))
	if e != nil {
		t.Fatal(e)
	}
	if key.OrgID != "" {
		if _, e = pool.Exec(t.Context(), `INSERT INTO orgs(id,slug,plan,deleted_pending,personal_org,personal_owner_account_id) VALUES($1,'personal','pro',false,true,$2)`, key.OrgID, acct.ID); e != nil {
			t.Fatal(e)
		}
	}
	(*env.S()).WithAdminAllowlist(acct.Email)
	if e = (*env.S()).ConfigureCustomerSupportForTest(t.Context(), pool); e != nil {
		t.Fatal(e)
	}
	headers := map[string]string{"Idempotency-Key": uuid.NewString()}
	response := env.Do(t, http.MethodPost, "/v1/support/tickets", api.CreateSupportTicketRequest{Subject: "Deployment issue", Category: "deployment", Body: "Please help"}, headers)
	if response.Code != 202 {
		t.Fatalf("create: %d %s", response.Code, response.Body)
	}
	var receipt api.SupportSubmission
	if e = json.Unmarshal(response.Body.Bytes(), &receipt); e != nil {
		t.Fatal(e)
	}
	if got := env.Do(t, http.MethodGet, "/v1/support/tickets/"+receipt.TicketID, nil, nil); got.Code != 200 {
		t.Fatalf("get: %d %s", got.Code, got.Body)
	}
	if got := env.Do(t, http.MethodGet, "/v1/admin/support/tickets", nil, nil); got.Code != 403 {
		t.Fatalf("staff bearer: %d %s", got.Code, got.Body)
	}
	if got := env.DoOperator(t, http.MethodGet, "/v1/admin/support/tickets", nil, nil); got.Code != 200 {
		t.Fatalf("staff session: %d %s", got.Code, got.Body)
	}
	payload := map[string]any{"expected_revision": 1, "body": "Try this setting"}
	if got := env.DoSession(t, "", http.MethodPost, "/v1/admin/support/tickets/"+receipt.TicketID+"/messages", payload, map[string]string{"Idempotency-Key": uuid.NewString()}); got.Code != 403 {
		t.Fatalf("missing fresh step-up: %d %s", got.Code, got.Body)
	}
	if got := env.DoAdmin(t, http.MethodPost, "/v1/admin/support/tickets/"+receipt.TicketID+"/messages", payload, map[string]string{"Idempotency-Key": uuid.NewString(), "Origin": "https://evil.example"}); got.Code != 403 {
		t.Fatalf("cross-origin staff write: %d %s", got.Code, got.Body)
	}
	if got := env.DoAdmin(t, http.MethodPost, "/v1/admin/support/tickets/"+receipt.TicketID+"/messages", payload, map[string]string{"Idempotency-Key": uuid.NewString()}); got.Code != 202 {
		t.Fatalf("fresh staff reply: %d %s", got.Code, got.Body)
	}
	if _, e = pool.Exec(t.Context(), `UPDATE accounts SET email_verified_at=NULL WHERE id=$1`, acct.ID); e != nil {
		t.Fatal(e)
	}
	if got := env.Do(t, http.MethodPost, "/v1/support/tickets", api.CreateSupportTicketRequest{Subject: "Deployment issue", Category: "deployment", Body: "Please help"}, headers); got.Code != 403 {
		t.Fatalf("revoked verification replay: %d %s", got.Code, got.Body)
	}
}
