package datapipeline

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeTenantRegrantAfterEmptyMembershipPreservesQuarantine(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,tenant_id text NOT NULL,status text);INSERT INTO public.orders VALUES(1,'a','a row'),(2,'b','b row')`, servingFixture())
	role := nativeReaderRole(t, f)
	approveTenantMembers(t, f, []api.DataPipelineTenantMembership{})
	f.tick(t)
	if nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("full revocation failed")
	}
	approveTenantMembers(t, f, []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "a"}})
	f.tick(t)
	if nativeReadAs(t, f, role, "alice") != 1 {
		t.Fatal("empty memberships were mistaken for a quarantined table")
	}
	if _, err := f.target.Exec(t.Context(), `ALTER POLICY gregale_pipeline_read ON public.orders USING(false)`); err != nil {
		t.Fatal(err)
	}
	approveTenantMembers(t, f, []api.DataPipelineTenantMembership{{Subject: "bob", Tenant: "a"}})
	f.tick(t)
	if nativeReadAs(t, f, role, "bob") != 0 {
		t.Fatal("reader change reopened actual quarantine")
	}
}
