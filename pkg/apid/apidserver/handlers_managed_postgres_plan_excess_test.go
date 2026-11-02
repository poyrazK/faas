// adr: 20261102125208 — databases past a lowered plan are suspended, not deleted.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// A Hobby account holding two databases, as after a subscription ends, sees
// the newer one past its plan on the list and the read while plan excess is
// enforced, and nothing while it is not.
func TestManagedPostgresViewsShowDatabasesPastThePlan(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	account := (*e.Acct()).ID
	service, first, _ := graceAPIService(t, account, graceTestSpec(managedpostgres.ClassDevelopment))
	second, err := service.Create(t.Context(), managedpostgres.CreateRequest{AccountID: account, Name: "events", Spec: graceTestSpec(managedpostgres.ClassDevelopment)})
	if err != nil {
		t.Fatal(err)
	}
	(*(*e.S()).ManagedPostgres()) = service
	databases, err := service.List(t.Context(), account)
	if err != nil {
		t.Fatal(err)
	}
	hobby, _ := api.ManagedPostgresLimitsFor(api.PlanHobby)
	want := managedpostgres.PlanExcess(databases, hobby)
	if len(want) != 1 || want[first.ID] == want[second.ID] {
		t.Fatalf("PlanExcess = %v; want exactly one of the two databases", want)
	}
	listed := func() map[string]string {
		t.Helper()
		rec := e.Do(t, http.MethodGet, "/v1/postgres/databases", nil, nil)
		var list api.ManagedPostgresDatabaseList
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil {
			t.Fatal("list", rec.Code, rec.Body.String())
		}
		got := map[string]string{}
		for _, item := range list.Items {
			if item.PlanExcess != "" {
				got[item.ID] = item.PlanExcess
			}
		}
		return got
	}
	read := func(id string) string {
		t.Helper()
		rec := e.Do(t, http.MethodGet, "/v1/postgres/databases/"+id, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatal("read", rec.Code, rec.Body.String())
		}
		return decodeManagedPostgresDatabase(t, rec.Body.Bytes()).PlanExcess
	}

	if got := listed(); len(got) != 0 || read(first.ID) != "" || read(second.ID) != "" {
		t.Fatalf("not enforced: list %v; want no plan_excess", got)
	}
	(*e.S()).SetManagedPostgresPlanExcessForTest(true)
	if got := listed(); len(got) != 1 || got[first.ID] != want[first.ID] || got[second.ID] != want[second.ID] {
		t.Fatalf("enforced: list %v; want %v", got, want)
	}
	for _, id := range []string{first.ID, second.ID} {
		if got := read(id); got != want[id] {
			t.Fatalf("read %s plan_excess = %q; want %q", id, got, want[id])
		}
	}
}
