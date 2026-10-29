package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A past-due company's apps keep serving (spec §4.7), so its credentials stay
// live: an admin must still be able to revoke a leaked consumer key and a
// developer to change configuration. Only deploys are refused, as for a
// past-due personal account. A suspended company's apps are parked and it is
// locked, as a suspended account is.
func TestPastDueCompanyRevokesCredentialsButCannotDeploy(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "past-due", "Past Due", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "dunning-api"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	_, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, asDeveloper := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	r := e.Do(t, http.MethodPost, "/v1/apps/dunning-api/consumers", api.CreateAPIConsumerRequest{ExternalRef: "partner", Name: "Partner"}, nil)
	var consumer api.APIConsumerResponse
	if r.Code != http.StatusCreated || json.Unmarshal(r.Body.Bytes(), &consumer) != nil {
		t.Fatalf("create consumer = %d %s", r.Code, r.Body)
	}
	r = e.Do(t, http.MethodPost, "/v1/apps/dunning-api/consumers/"+consumer.ID+"/keys", api.CreateConsumerKeyRequest{Name: "partner", Scopes: []string{"read"}}, nil)
	var key api.ConsumerKeyResponse
	if r.Code != http.StatusCreated || json.Unmarshal(r.Body.Bytes(), &key) != nil {
		t.Fatalf("create consumer key = %d %s", r.Code, r.Body)
	}
	leaked := "/v1/apps/dunning-api/consumers/" + consumer.ID + "/keys/" + key.ID

	if err := store.UpdateOrgStatus(ctx, org.ID, state.OrgStatusPastDue); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodDelete, leaked, nil, asAdmin); r.Code != http.StatusOK {
		t.Fatalf("past-due admin revoking a consumer key = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPut, "/v1/apps/dunning-api/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "debug"}, asDeveloper); r.Code != http.StatusOK {
		t.Fatalf("past-due developer changing configuration = %d %s", r.Code, r.Body)
	}
	deploy := api.CreateDeploymentRequest{Image: "registry.example/app@sha256:" + strings.Repeat("a", 64)}
	if r := e.Do(t, http.MethodPost, "/v1/apps/dunning-api/deployments", deploy, nil); r.Code != http.StatusPaymentRequired || !strings.Contains(r.Body.String(), api.CodeBillingPastDue) {
		t.Fatalf("past-due company deploy = %d %s; want 402 %s", r.Code, r.Body, api.CodeBillingPastDue)
	}

	if err := store.UpdateOrgStatus(ctx, org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodPut, "/v1/apps/dunning-api/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "info"}, asAdmin); r.Code != http.StatusForbidden {
		t.Fatalf("suspended admin changing configuration = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodGet, "/v1/apps/dunning-api", nil, asDeveloper); r.Code != http.StatusPaymentRequired {
		t.Fatalf("suspended developer reading the app = %d %s", r.Code, r.Body)
	}
}
