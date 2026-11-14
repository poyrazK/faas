// adr: 20261111140000

package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An operator makes an account complimentary on a paid plan, which holds its
// spend cap at $0 so it stops at the plan's allowances; plan=none returns it
// to Free with no cap. A subscribed account is refused.
func TestObsAccountComplimentary(t *testing.T) {
	ctx := context.Background()
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	store := *e.Store()
	var comps state.ComplimentaryAccountStore = store
	acct, err := store.CreateAccount(ctx, "partner@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	path := func(id, query string) string {
		return "/v1/admin/ops/accounts/" + id + "/complimentary?confirm=true&reason=partner_program" + query
	}

	for _, query := range []string{"&plan=free", "&plan=enterprise", "&plan=none&until=2099-01-01T00:00:00Z", "&plan=pro&until=2001-01-01T00:00:00Z", "&plan=pro&until=tomorrow"} {
		apidserver.AssertProblem(t, e.DoAdmin(t, http.MethodPost, path(acct.ID, query), nil, nil), http.StatusBadRequest, api.CodeValidation)
	}
	apidserver.AssertProblem(t, e.DoAdmin(t, http.MethodPost, "/v1/admin/ops/accounts/"+acct.ID+"/complimentary?reason=partner_program&plan=pro", nil, nil), http.StatusBadRequest, api.CodeValidation)

	until := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Second)
	rec := e.DoAdmin(t, http.MethodPost, path(acct.ID, "&plan=pro&until="+until.Format(time.RFC3339)), nil, nil)
	var view api.ObsComplimentaryResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	if !view.Complimentary || view.Plan != api.PlanPro || view.ExpiresAt == nil || !view.ExpiresAt.Equal(until) {
		t.Fatalf("set view = %+v", view)
	}
	got, err := store.AccountByID(ctx, acct.ID)
	if err != nil || got.Plan != api.PlanPro {
		t.Fatalf("plan after set = %q, %v", got.Plan, err)
	}
	if cents, capped, err := store.GetAccountOverageCapCents(ctx, acct.ID); err != nil || !capped || cents != 0 {
		t.Fatalf("spend cap after set = %d (capped %v), %v; want $0", cents, capped, err)
	}
	if row, err := comps.ComplimentaryAccount(ctx, acct.ID); err != nil || row == nil || row.GrantedBy != (*e.Acct()).ID || row.Reason != "partner_program" {
		t.Fatalf("row after set = %+v, %v", row, err)
	}

	rec = e.DoAdmin(t, http.MethodPost, path(acct.ID, "&plan=none"), nil, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.Complimentary || view.Plan != api.PlanFree {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := store.AccountByID(ctx, acct.ID); got.Plan != api.PlanFree {
		t.Fatalf("plan after clear = %q", got.Plan)
	}
	if _, capped, _ := store.GetAccountOverageCapCents(ctx, acct.ID); capped {
		t.Fatal("a cleared account keeps the $0 cap")
	}
	if row, _ := comps.ComplimentaryAccount(ctx, acct.ID); row != nil {
		t.Fatalf("row after clear = %+v", row)
	}

	subscribed, err := store.CreateAccount(ctx, "subscribed@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, subscribed.ID, "sub_123"); err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, e.DoAdmin(t, http.MethodPost, path(subscribed.ID, "&plan=scale"), nil, nil), http.StatusConflict, api.CodeConflict)
	apidserver.AssertProblem(t, e.DoAdmin(t, http.MethodPost, path("00000000-0000-0000-0000-00000000abcd", "&plan=pro"), nil, nil), http.StatusNotFound, api.CodeNotFound)
}
