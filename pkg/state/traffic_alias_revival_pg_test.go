//go:build !no_pg

// adr: 570
package state

import (
	"encoding/json"
	"testing"
)

func TestPgTrafficAliasDeploymentRevivalRollback(t *testing.T) {
	registerTrafficRevivalCapture(t)
	for _, mode := range trafficAliasRevivalModes {
		t.Run(mode, func(t *testing.T) {
			_, pool, account, app := trafficHostPGFixture(t)
			store := NewPgStore(pool, WithTrafficAppsDomain("apps.example.test"))
			testTrafficAliasRevival(t, store, store, app, mode, func(candidate Deployment, source App, host string) {
				if mode != "unchanged" && mode != "dark" && mode != "canceled-analysis" {
					status := DeployFailed
					if mode == "cancelled" {
						status = DeployCancelled
					}
					if _, err := pool.Exec(t.Context(), `UPDATE deployments SET status=$2 WHERE id=$1`, candidate.ID, string(status)); err != nil {
						t.Fatal(err)
					}
				}
				in := globalTrafficRule(account, source, host, 520)
				encoded, err := json.Marshal(in.Action)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES('00000000-0000-0000-0000-000000000376',$1,$2,$3,'/',true,'route',$4::jsonb)`, account.ID, source.ID, host, encoded); err != nil {
					t.Fatal(err)
				}
			}, func() string {
				var result string
				err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
				    'deployments',(SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM deployments d WHERE app_id=$1),
				    'aliases',(SELECT jsonb_agg(to_jsonb(z) ORDER BY name) FROM deployment_aliases z WHERE app_id=$1),
				    'crons',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM crons c WHERE app_id=$1),
				    'snapshots',(SELECT jsonb_agg(to_jsonb(s) ORDER BY deployment_id) FROM deployment_openapi_snapshots s WHERE app_id=$1),
				    'activity',(SELECT count(*) FROM org_activity_outbox),
				    'deliveries',(SELECT count(*) FROM app_webhook_deliveries))::text`, app.ID).Scan(&result)
				if err != nil {
					t.Fatal(err)
				}
				return result
			})
		})
	}
}
