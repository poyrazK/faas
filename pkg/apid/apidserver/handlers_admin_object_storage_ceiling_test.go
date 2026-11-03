// adr: 20261101214152

package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Support raises a paid account's object-storage ceiling above its plan's, the
// spend cap still tightens it, gib=plan returns it to the plan's, and a Free
// account is refused: its storage is never billed.
func TestObsObjectStorageCeiling(t *testing.T) {
	const gib = int64(1 << 30)
	ctx := context.Background()
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	store := *e.Store()
	var grants state.ObjectStorageCeilingGrantStore = store
	pro, err := store.CreateAccount(ctx, "tenant-storage@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/admin/ops/accounts/" + pro.ID + "/object-storage-ceiling?confirm=true&reason=support_ticket"

	apidserver.AssertProblem(t, e.DoAdmin(t, http.MethodPost, "/v1/admin/ops/accounts/"+pro.ID+"/object-storage-ceiling?gib=500&reason=support_ticket", nil, nil), http.StatusBadRequest, api.CodeValidation)
	apidserver.AssertProblem(t, e.DoAdmin(t, http.MethodPost, base+"&gib=0", nil, nil), http.StatusBadRequest, api.CodeValidation)

	rec := e.DoAdmin(t, http.MethodPost, base+"&gib=500", nil, nil)
	var view api.ObsObjectStorageCeilingResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil {
		t.Fatalf("grant 500 GiB: %d %s", rec.Code, rec.Body.String())
	}
	if view.Plan != api.PlanPro || view.PlanCeilingBytes != 250*gib || view.GrantBytes == nil || *view.GrantBytes != 500*gib || view.CeilingBytes != 500*gib {
		t.Fatalf("grant 500 GiB view = %+v", view)
	}
	if got, err := grants.ObjectStorageCeilingGrant(ctx, pro.ID); err != nil || got == nil || got.MaxBytes != 500*gib || got.Reason != "support_ticket" || got.GrantedBy != (*e.Acct()).ID {
		t.Fatalf("stored grant = %+v, %v", got, err)
	}

	// Pro's default $10 cap pays for 383 GiB, below the grant.
	proCap, _ := api.PlanPro.DefaultOverageCapCents()
	if err := store.UpdateAccountOverageCapCents(ctx, pro.ID, &proCap); err != nil {
		t.Fatal(err)
	}
	rec = e.DoAdmin(t, http.MethodPost, base+"&gib=500", nil, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.CeilingBytes != 383*gib {
		t.Fatalf("grant under the default cap: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.DoAdmin(t, http.MethodPost, base+"&gib=plan", nil, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.GrantBytes != nil || view.CeilingBytes != 250*gib {
		t.Fatalf("back to the plan's ceiling: %d %s", rec.Code, rec.Body.String())
	}
	if got, err := grants.ObjectStorageCeilingGrant(ctx, pro.ID); err != nil || got != nil {
		t.Fatalf("grant after gib=plan = %+v, %v", got, err)
	}

	free, err := store.CreateAccount(ctx, "tenant-free-storage@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, e.DoAdmin(t, http.MethodPost, "/v1/admin/ops/accounts/"+free.ID+"/object-storage-ceiling?confirm=true&reason=support_ticket&gib=50", nil, nil), http.StatusConflict, api.CodeConflict)
	apidserver.AssertProblem(t, e.DoAdmin(t, http.MethodPost, "/v1/admin/ops/accounts/00000000-0000-0000-0000-00000000abcd/object-storage-ceiling?confirm=true&reason=support_ticket&gib=50", nil, nil), http.StatusNotFound, api.CodeNotFound)
}

// A grant above the installation's max_account_bytes would never take effect,
// because every account is held to the tighter of the two, so it is refused.
func TestObsObjectStorageCeilingAboveInstallationLimit(t *testing.T) {
	ctx := context.Background()
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	// ObjectRegistry's safety policy holds every account to 1,000 bytes.
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, &apidserver.FakeObjectProvider{}, &apidserver.FakeObjectProvider{}, "external"))
	pro, err := (*e.Store()).CreateAccount(ctx, "tenant-big-storage@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/admin/ops/accounts/" + pro.ID + "/object-storage-ceiling?confirm=true&reason=support_ticket"
	apidserver.AssertProblem(t, e.DoAdmin(t, http.MethodPost, base+"&gib=500", nil, nil), http.StatusConflict, api.CodeConflict)
	if got, err := (*e.Store()).ObjectStorageCeilingGrant(ctx, pro.ID); err != nil || got != nil {
		t.Fatalf("refused grant was stored: %+v, %v", got, err)
	}
	if rec := e.DoAdmin(t, http.MethodPost, base+"&gib=plan", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("returning to the plan's ceiling is always allowed: %d %s", rec.Code, rec.Body.String())
	}
}
