package db_test

// adr: 595, 20261031190030

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// An image rescan renewed the deployment's scan mirrors, and the runtime input
// guard refused the next thaw or warm promotion of every ordinary instance of
// it (production 2026-10-31: ~5% of thaws). The guard now leaves the mirrors
// out; everything the instance runs, and a regressed scan's parked_reason,
// stays compared.
func TestRuntimeGuardComparisonIgnoresImageRescans(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	const captured = `{"app_id":"a","account_plan":"pro","instance_mode":"normal","adoptions":[],"materialized_fields":[],
		"artifact":{"id":"d","scope":"production","scan_status":"passed","scan_result_hash":"h1","parked_reason":null,"rootfs_key":"k"}}`
	for _, tc := range []struct {
		name, current string
		want          bool
	}{
		{"unchanged", captured, true},
		{"rescanned", `{"app_id":"a","account_plan":"pro","instance_mode":"normal","adoptions":[],"materialized_fields":[],
			"artifact":{"id":"d","scope":"production","scan_status":"failed","scan_result_hash":"h2","parked_reason":null,"rootfs_key":"k"}}`, true},
		{"scan regressed", `{"app_id":"a","account_plan":"pro","instance_mode":"normal","adoptions":[],"materialized_fields":[],
			"artifact":{"id":"d","scope":"production","scan_status":"failed","scan_result_hash":"h2","parked_reason":"security_scan_regressed","rootfs_key":"k"}}`, false},
		{"artifact bytes changed", `{"app_id":"a","account_plan":"pro","instance_mode":"normal","adoptions":[],"materialized_fields":[],
			"artifact":{"id":"d","scope":"production","scan_status":"passed","scan_result_hash":"h1","parked_reason":null,"rootfs_key":"k2"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got bool
			if err := pool.QueryRow(t.Context(), `SELECT application_standard_runtime_inputs_match($1::jsonb, $2::jsonb)`,
				captured, tc.current).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("match = %v, want %v", got, tc.want)
			}
		})
	}
}
