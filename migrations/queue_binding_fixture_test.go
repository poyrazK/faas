//go:build !no_pg

package migrations_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

// Historical fixtures must use the historical schema, rather than today's
// sqlc adapters, which read columns added by later migrations.
func seedLegacyQueueBindingConsumer(t *testing.T, pool *pgxpool.Pool, account, app, name string, enabled bool) state.QueueBindingConsumerResult {
	t.Helper()
	result := state.QueueBindingConsumerResult{Binding: state.QueueBinding{AccountID: account, AppID: app, Name: name, QueueName: name, Mode: "push", Enabled: enabled, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1}}
	if err := pool.QueryRow(t.Context(), `insert into queue_bindings(account_id,app_id,name,queue_name,mode,workload_class,enabled,max_concurrency)
 values($1,$2,$3,$3,'push','worker',$4,1) returning id`, account, app, name, enabled).Scan(&result.Binding.ID); err != nil {
		t.Fatal(err)
	}
	var trigger string
	if err := pool.QueryRow(t.Context(), `insert into triggers(account_id,app_id,queue_binding_id,kind,source,slug,enabled,config)
 values($1,$2,$3,'queue','queue',$4,$5,jsonb_build_object('mode','queue','queue_binding_id',$3::uuid::text)) returning id`, account, app, result.Binding.ID, name, enabled).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	result.Changes = []state.QueueConsumerChange{{Kind: "created", AppID: app, TriggerID: trigger}}
	return result
}
