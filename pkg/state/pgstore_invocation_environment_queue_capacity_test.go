//go:build !no_pg

// adr: 590
package state_test

import "testing"

func TestPgEnvironmentQueueProducerDepthIsAtomicAndIsolated(t *testing.T) {
	store, _, pool := pgWithPool(t)
	testEnvironmentQueueProducerDepth(t, store, func(t *testing.T, id string, n int) {
		if _, err := pool.Exec(t.Context(), `INSERT INTO invocations
 (app_id,account_id,org_id,source,state,payload,headers,due_at,method,path,queue_name,deployment_scope,environment_id,queue_binding_id,platform_tenant_id)
SELECT app_id,account_id,org_id,source,state,payload,headers,due_at,method,path,queue_name,deployment_scope,environment_id,queue_binding_id,platform_tenant_id
FROM invocations, generate_series(1,$2) WHERE id=$1`, id, n); err != nil {
			t.Fatal(err)
		}
	})
}
