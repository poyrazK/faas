// adr: 20261031090658 — an account owns a bounded number of Free organizations.
package apidserver_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A new organization starts on Free and owns its resources, so each one is
// another Free app and allowance. The second Free organization is refused
// until the first is upgraded.
func TestCreateOrgRefusesAnotherFreeOrganization(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	create := func(slug string) (int, []byte) {
		t.Helper()
		rec := e.Do(t, "POST", "/v1/orgs", map[string]any{"slug": slug, "name": slug}, nil)
		return rec.Code, rec.Body.Bytes()
	}
	code, body := create("free-one")
	if code != 201 {
		t.Fatalf("first Free organization: %d %s", code, body)
	}
	var first api.OrgResponse
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}

	code, body = create("free-two")
	var problem api.Problem
	if code != 403 || json.Unmarshal(body, &problem) != nil || problem.Code != api.CodeOrgFreeLimitExceeded ||
		problem.Limit == nil || *problem.Limit != api.FreeOrganizationsOwnedMax || problem.Observed == nil || *problem.Observed != 1 {
		t.Fatalf("second Free organization: %d %s", code, body)
	}
	if _, err := store.OrgBySlug(t.Context(), "free-two"); err == nil {
		t.Fatal("refused organization was created")
	}

	if err := store.UpdateOrgPlan(t.Context(), first.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	if code, body := create("free-two"); code != 201 {
		t.Fatalf("Free organization after upgrading the first: %d %s", code, body)
	}
}
