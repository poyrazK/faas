//go:build !no_pg

// adr: 570
package state

import "testing"

func TestPgTrafficEnvironmentRemoval(t *testing.T) {
	store, pool, account, _ := trafficHostPGFixture(t)
	peerAccount, peer := globalTrafficPeer(t, store, "environment-removal-peer")
	testTrafficEnvironmentRemoval(t, store, account, peerAccount, peer, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string {
		var environments string
		if err := pool.QueryRow(t.Context(), `SELECT md5(coalesce((SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM project_environments t),'null')::text)`).Scan(&environments); err != nil {
			t.Fatal(err)
		}
		return pgTrafficAppBindingIntent(t, pool) + environments
	})
}

func TestPgTrafficDNSDriftWithdrawal(t *testing.T) {
	store, pool, account, _ := trafficHostPGFixture(t)
	peerAccount, peer := globalTrafficPeer(t, store, "dns-drift-peer")
	testTrafficDNSDriftWithdrawal(t, store, account, peerAccount, peer, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) })
}
