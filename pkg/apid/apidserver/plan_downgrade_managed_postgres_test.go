// adr: 20261031091607 — a downgrade waits until the managed PostgreSQL
// databases fit the target plan: their count and their service class.
package apidserver_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestPlanDowngradeRefusedUntilDatabasesFit(t *testing.T) {
	env := apidserver.SetupPGHandler(t, api.PlanPro)
	pool, srv := *env.Pool(), *env.S()
	store, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "fixture"},
		Backends: []managedpostgres.BackendConfig{{ID: "fixture", Driver: "fixture", Region: "us-east-1", Namespace: "fixture"}}}, func(string) string { return "" },
		map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return capabilityOnlyProvider{}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	*srv.ManagedPostgres() = service
	account, err := (*env.Store()).AccountByID(t.Context(), env.Acct().ID)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(name, class, state string) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint,accounting_required,state)
VALUES($1,$2,$3,'us-east-1',17,$4,'single_zone','fixture',$5,false,$6)`, uuid.NewString(), account.ID, name, class, strings.Repeat("a", 64), state); err != nil {
			t.Fatal(err)
		}
	}
	insert("orders", "development", "provisioning")
	if problem := srv.PlanDowngradeProblemForTest(t.Context(), account, api.PlanHobby); problem != nil {
		t.Fatalf("one development database fits Hobby: %+v", problem)
	}
	insert("events", "burstable", "provisioning")
	problem := srv.PlanDowngradeProblemForTest(t.Context(), account, api.PlanHobby)
	if problem == nil || problem.Code != "managed_postgres_quota_exceeded" {
		t.Fatalf("two databases on Hobby = %+v, want managed_postgres_quota_exceeded", problem)
	}
	for _, task := range []string{"delete 1 managed PostgreSQL database(s): the hobby plan allows 1 and you have 2", "resize database events from the burstable class"} {
		if !strings.Contains(problem.Detail, task) {
			t.Fatalf("detail %q lacks %q", problem.Detail, task)
		}
	}
	// A database whose delete is scheduled no longer counts.
	if _, err := pool.Exec(t.Context(), `UPDATE managed_postgres_databases SET state='deleting' WHERE name='events'`); err != nil {
		t.Fatal(err)
	}
	if problem := srv.PlanDowngradeProblemForTest(t.Context(), account, api.PlanHobby); problem != nil {
		t.Fatalf("a scheduled delete must not hold the downgrade: %+v", problem)
	}
}
