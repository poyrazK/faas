package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A company app's credentials keep working after the member who minted them
// leaves, and that member saw each secret once. The company's admins must be
// able to find and revoke them: a consumer API key and an inbound-webhook
// signing secret the creator set up, after the creator is removed. Developers
// still cannot.
//
// adr: 20261026151103
func TestAdminsRevokeADepartedCreatorsCompanyAppCredentials(t *testing.T) {
	useTestSealRecipient(t)
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	creator := *e.Acct()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "credentials", "Credentials", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "keys-api"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	decode := func(r interface{ Bytes() []byte }, out any) {
		t.Helper()
		if err := json.Unmarshal(r.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	r := e.Do(t, http.MethodPost, "/v1/apps/keys-api/consumers", api.CreateAPIConsumerRequest{ExternalRef: "partner", Name: "Partner"}, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("create consumer = %d %s", r.Code, r.Body)
	}
	var consumer api.APIConsumerResponse
	decode(r.Body, &consumer)
	r = e.Do(t, http.MethodPost, "/v1/apps/keys-api/consumers/"+consumer.ID+"/keys", api.CreateConsumerKeyRequest{Name: "partner", Scopes: []string{"admin"}}, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("create consumer key = %d %s", r.Code, r.Body)
	}
	var key api.ConsumerKeyResponse
	decode(r.Body, &key)
	hook := mustCreateInboundWebhook(t, e, "keys-api")

	admin, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, asDeveloper := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(ctx, org.ID, creator.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+creator.ID, nil, asAdmin); r.Code >= 300 {
		t.Fatalf("remove creator = %d %s", r.Code, r.Body)
	}

	keyPath := "/v1/apps/keys-api/consumers/" + consumer.ID + "/keys/" + key.ID
	hookPath := "/v1/apps/keys-api/inbound-webhooks/" + hook.ID
	for _, path := range []string{keyPath, hookPath} {
		if r := e.Do(t, http.MethodDelete, path, nil, asDeveloper); r.Code != http.StatusForbidden {
			t.Fatalf("developer DELETE %s = %d %s", path, r.Code, r.Body)
		}
	}

	r = e.Do(t, http.MethodGet, "/v1/apps/keys-api/consumers/"+consumer.ID+"/keys", nil, asAdmin)
	if r.Code != http.StatusOK {
		t.Fatalf("admin listing the departed creator's consumer keys = %d %s", r.Code, r.Body)
	}
	var keys api.ConsumerKeyListResponse
	decode(r.Body, &keys)
	if len(keys.Keys) != 1 || keys.Keys[0].ID != key.ID || keys.Keys[0].Key != "" {
		t.Fatalf("admin's key list = %+v", keys)
	}
	if r := e.Do(t, http.MethodDelete, keyPath, nil, asAdmin); r.Code != http.StatusOK {
		t.Fatalf("admin revoking the departed creator's consumer key = %d %s", r.Code, r.Body)
	}
	if row, err := store.GetConsumerKeyByID(ctx, creator.ID, key.ID); err != nil || row.RevokedAt == nil {
		t.Fatalf("consumer key after the admin revoked it = %+v, %v", row, err)
	}

	if r := e.Do(t, http.MethodGet, "/v1/apps/keys-api/inbound-webhooks", nil, asAdmin); r.Code != http.StatusOK {
		t.Fatalf("admin listing inbound webhooks = %d %s", r.Code, r.Body)
	}
	secret := "whsec_rotated_by_the_company"
	if r := e.Do(t, http.MethodPatch, hookPath, api.UpdateInboundWebhookEndpointRequest{SigningSecret: &secret}, asAdmin); r.Code != http.StatusOK {
		t.Fatalf("admin rotating the inbound webhook's signing secret = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodDelete, hookPath, nil, asAdmin); r.Code != http.StatusNoContent {
		t.Fatalf("admin deleting the inbound webhook = %d %s", r.Code, r.Body)
	}
}
