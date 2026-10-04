//go:build !no_pg

// adr: 531
package state

import "testing"

func TestPgTrafficAppBatchWithdrawal(t *testing.T) {
	for _, mode := range []string{"project", "preview"} {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, _ := trafficHostPGFixture(t)
			testTrafficAppBatchWithdrawal(t, store, account, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string {
				var digest string
				err := pool.QueryRow(t.Context(), `SELECT md5(jsonb_build_object(
     'project',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM projects t),
     'preview',(SELECT jsonb_agg(to_jsonb(t) ORDER BY installation_id,repo_full_name,pr_number) FROM pr_preview_sets t)
    )::text)`).Scan(&digest)
				if err != nil {
					t.Fatal(err)
				}
				return pgTrafficAppBindingIntent(t, pool) + digest
			})
		})
	}
}
