//go:build !no_pg

// adr: 531
package state

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func pgTrafficAppBindingIntent(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var digest string
	err := pool.QueryRow(t.Context(), `SELECT md5(jsonb_build_object(
  'apps',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM apps t),
  'crons',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM crons t),
  'tasks',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM app_tasks t),
  'deployments',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM deployments t),
  'builds',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM builds t),
  'cleanup',(SELECT jsonb_agg(to_jsonb(t) ORDER BY build_id) FROM builder_vm_cleanup t),
  'snapshots',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM snapshots t),
  'invocations',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM invocations t),
  'async_quota',(SELECT jsonb_agg(to_jsonb(t) ORDER BY account_id) FROM account_async_quota t),
  'domains',(SELECT jsonb_agg(to_jsonb(t) ORDER BY domain) FROM custom_domains t),
  'default_domains',(SELECT jsonb_agg(to_jsonb(t) ORDER BY app_id) FROM app_default_domains t),
  'surfaces',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM tenant_surfaces t),
  'hostnames',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM tenant_hostnames t),
  'activity',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM org_activity_outbox t)
 )::text)`).Scan(&digest)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestPgTrafficAppBindingWithdrawals(t *testing.T) {
	for _, mode := range trafficAppWithdrawalModes {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			testTrafficAppWithdrawal(t, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAppBindingIntent(t, pool) })
		})
	}
}

func TestPgTrafficAppPurgeFallback(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	testTrafficAppPurgeFallback(t, store, account, app, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAppBindingIntent(t, pool) })
}

func TestPgTrafficAppPurgeForeignRedirect(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	testTrafficAppPurgeForeignRedirect(t, store, account, app, func(host, id string) {
		if _, err := pool.Exec(t.Context(), `UPDATE custom_domains SET app_id_redirect=$2::uuid WHERE domain=$1::citext`, host, id); err != nil {
			t.Fatal(err)
		}
	}, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAppBindingIntent(t, pool) })
}

func TestPgTrafficAppPurgeGlobalReservations(t *testing.T) {
	for _, kind := range []string{"domain", "deleted-tenant"} {
		t.Run(kind, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			testTrafficAppPurgeGlobalReservation(t, store, account, app, kind, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAppBindingIntent(t, pool) })
		})
	}
}

func TestPgTrafficAppRenamePublication(t *testing.T) {
	_, pool, account, app := trafficHostPGFixture(t)
	store := NewPgStore(pool, WithTrafficAppsDomain(".apps.example.test"))
	testTrafficAppRenamePublication(t, store, account, app, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAppBindingIntent(t, pool) })
}
