// adr: 20261030093512 — an organization billing subject has its own spend cap.
package apidserver_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An organization that owns its resources is billed apart from its members,
// so it has its own spend cap: the owner sets it, any member reads it, and
// setting it never touches the caller's personal cap.
func TestOrgSpendCapRoutes(t *testing.T) {
	ctx := t.Context()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "cap-company", "Cap Company", api.PlanPro)
	personal, personalSet, err := store.GetAccountOverageCapCents(ctx, e.Acct().ID)
	if err != nil {
		t.Fatal(err)
	}
	read := func(headers map[string]string) api.AccountOverageCapResponse {
		t.Helper()
		rec := e.Do(t, "GET", "/v1/orgs/"+org.Slug+"/overage-cap", nil, headers)
		var out api.AccountOverageCapResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("read organization cap: %d %s", rec.Code, rec.Body)
		}
		return out
	}

	rec := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/overage-cap", map[string]any{"overage_cap_cents": 2500}, nil)
	var set api.AccountOverageCapResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &set) != nil || set.OverageCapCents == nil || *set.OverageCapCents != 2500 {
		t.Fatalf("set organization cap: %d %s", rec.Code, rec.Body)
	}
	if cents, ok, err := store.GetAccountOverageCapCents(ctx, org.ID); err != nil || !ok || cents != 2500 {
		t.Fatalf("stored organization cap = %d, %v, %v; want 2500", cents, ok, err)
	}
	if got := read(nil); got.OverageCapCents == nil || *got.OverageCapCents != 2500 {
		t.Fatalf("read organization cap = %+v, want 2500", got)
	}
	if cents, ok, err := store.GetAccountOverageCapCents(ctx, e.Acct().ID); err != nil || ok != personalSet || cents != personal {
		t.Fatalf("personal cap changed to %d, %v, %v", cents, ok, err)
	}

	_, viewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	if got := read(viewer); got.OverageCapCents == nil || *got.OverageCapCents != 2500 {
		t.Fatalf("viewer read = %+v, want 2500", got)
	}
	if rec := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/overage-cap", map[string]any{"overage_cap_cents": nil}, viewer); rec.Code != 403 {
		t.Fatalf("viewer cleared the organization cap: %d %s", rec.Code, rec.Body)
	}

	rec = e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/overage-cap", map[string]any{"overage_cap_cents": nil}, nil)
	if rec.Code != 200 || read(nil).OverageCapCents != nil {
		t.Fatalf("clear organization cap: %d %s", rec.Code, rec.Body)
	}
}
