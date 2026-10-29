//go:build !no_pg

// adr: 20261027163626 — deploy create against a production-sized fleet: the
// rollout's benchmark at 10,000 apps and 1,000 route-owning accounts.
package state

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const (
	trafficScaleAccounts      = 2_000
	trafficScaleAppsEach      = 5
	trafficScaleRouteAccounts = 1_000
	trafficScaleAppsSuffix    = ".apps.example.test"
)

// TestPgTrafficGuardFleetScale is kept out of the normal unit gate: it seeds
// a production-sized fleet. scripts/bench-traffic-guard.sh runs it.
func TestPgTrafficGuardFleetScale(t *testing.T) {
	if os.Getenv("FAAS_RUN_TRAFFIC_GUARD_SCALE") != "1" {
		t.Skip("set FAAS_RUN_TRAFFIC_GUARD_SCALE=1 to run the traffic guard fleet benchmark")
	}
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	seed := NewPgStore(pool, WithTrafficAppsDomain(trafficScaleAppsSuffix))
	start := time.Now()
	accounts := make([]string, trafficScaleAccounts)
	for i := range accounts {
		account, err := seed.CreateAccount(ctx, fmt.Sprintf("scale-%d@example.test", i), api.PlanScale)
		if err != nil {
			t.Fatal(err)
		}
		accounts[i] = account.ID
	}
	template, err := seed.CreateApp(ctx, App{AccountID: accounts[0], Slug: "scale-template", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	// Clone the template row so every column CreateApp fills is set; only
	// the identity and owner differ. A NULL org takes the account's
	// personal organization.
	if _, err := pool.Exec(ctx, `
		INSERT INTO apps SELECT (jsonb_populate_record(t, jsonb_build_object(
		    'id', gen_random_uuid(), 'account_id', a.id, 'org_id', NULL, 'slug', 'scale-' || a.n || '-' || k,
		    'service_address_index', 100 + k,
		    'created_at', now(), 'updated_at', now()))).*
		FROM apps t,
		     (SELECT id, row_number() OVER (ORDER BY id) AS n FROM unnest($2::uuid[]) AS id) a,
		     generate_series(1, $3::int) AS k
		WHERE t.id = $1`, template.ID, accounts, trafficScaleAppsEach); err != nil {
		t.Fatalf("clone apps: %v", err)
	}
	// One verified custom domain on the first app of each account, and one
	// route per route-owning account: exact hosts, and a tenth subtrees.
	if _, err := pool.Exec(ctx, `
		INSERT INTO custom_domains(domain, app_id, challenge_token, verified_at)
		SELECT 'd' || row_number() OVER (ORDER BY a.account_id) || '.example.org', a.id, 'token', now()
		FROM (SELECT DISTINCT ON (account_id) account_id, id FROM apps WHERE slug LIKE 'scale-%' ORDER BY account_id, slug) a`); err != nil {
		t.Fatalf("seed domains: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO edge_rules(account_id, app_id, match_host, match_path, enabled, kind, action)
		SELECT a.account_id, a.id,
		       CASE WHEN a.n % 10 = 0 THEN '*.s' || a.n || '.routes.example.net' ELSE 'r' || a.n || '.routes.example.net' END,
		       '/', true, 'route', jsonb_build_object('kind', 'route', 'route', jsonb_build_object('target_app_slug', a.slug))
		FROM (SELECT account_id, id, slug, row_number() OVER (ORDER BY account_id) AS n
		      FROM (SELECT DISTINCT ON (account_id) account_id, id, slug FROM apps WHERE slug LIKE 'scale-%' ORDER BY account_id, slug) first) a
		WHERE a.n <= $1`, trafficScaleRouteAccounts); err != nil {
		t.Fatalf("seed routes: %v", err)
	}
	var apps, domains, routeOwners int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM apps), (SELECT count(*) FROM custom_domains),
		(SELECT count(DISTINCT account_id) FROM edge_rules WHERE kind = 'route' AND enabled)`).Scan(&apps, &domains, &routeOwners); err != nil {
		t.Fatal(err)
	}
	t.Logf("traffic guard scale: apps=%d domains=%d route_accounts=%d seeded in %s", apps, domains, routeOwners, time.Since(start).Round(time.Second))

	// Deploys go to apps of accounts that own no route, like most deploys.
	rows, err := pool.Query(ctx, `SELECT a.id FROM apps a WHERE a.slug LIKE 'scale-%'
		AND NOT EXISTS (SELECT 1 FROM edge_rules r WHERE r.account_id = a.account_id) ORDER BY a.id`)
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, id)
	}
	rows.Close()

	// What each guarded binding write reads three times: every claim in the
	// fleet, whatever the write's scope.
	var claimReads []time.Duration
	claimCount := 0
	for range 20 {
		began := time.Now()
		claims, err := readTrafficBindingClaims(ctx, pool, trafficScaleAppsSuffix)
		if err != nil {
			t.Fatal(err)
		}
		claimReads = append(claimReads, time.Since(began))
		claimCount = len(claims.Domains) + len(claims.Tenants) + len(claims.Aliases) + len(claims.Primaries)
	}
	t.Logf("traffic guard scale: fleet claims read claims=%d p50=%s p99=%s", claimCount, trafficScalePercentile(claimReads, 50), trafficScalePercentile(claimReads, 99))

	var mu sync.Mutex
	phases := map[string][]time.Duration{}
	SetTrafficPolicyPhaseObserver(func(writer, phase string, elapsed time.Duration) {
		if writer == TrafficPolicyWriterAppBinding {
			mu.Lock()
			phases[phase] = append(phases[phase], elapsed)
			mu.Unlock()
		}
	})
	t.Cleanup(func() { SetTrafficPolicyPhaseObserver(nil) })
	for _, run := range []struct {
		scoped  bool
		samples int
	}{{false, 20}, {true, 200}} {
		store := NewPgStore(pool, WithTrafficAppsDomain(trafficScaleAppsSuffix), WithTrafficScopedBindingGuard(run.scoped))
		mu.Lock()
		clear(phases)
		mu.Unlock()
		var latencies []time.Duration
		refused := map[string]int{}
		for i := range run.samples {
			began := time.Now()
			_, err := store.CreateDeployment(ctx, Deployment{AppID: targets[(i*97)%len(targets)], Kind: DeploymentKindImage, Status: DeployBuilding})
			latencies = append(latencies, time.Since(began))
			var analysis *TrafficPolicyAnalysisError
			switch {
			case errors.As(err, &analysis):
				refused[analysis.Scope]++
			case err != nil:
				t.Fatalf("deploy create: %v", err)
			}
		}
		mu.Lock()
		var breakdown []string
		for _, phase := range []string{TrafficPolicyPhaseDiscover, TrafficPolicyPhaseLockWait, TrafficPolicyPhaseBefore, TrafficPolicyPhaseValidate} {
			breakdown = append(breakdown, fmt.Sprintf("%s p50=%s p99=%s", phase, trafficScalePercentile(phases[phase], 50), trafficScalePercentile(phases[phase], 99)))
		}
		mu.Unlock()
		t.Logf("traffic guard scale: guard=%s deploys=%d p50=%s p99=%s max=%s refused=%v",
			trafficGuardName(run.scoped), len(latencies), trafficScalePercentile(latencies, 50), trafficScalePercentile(latencies, 99),
			slices.Max(latencies).Round(time.Millisecond), refused)
		t.Logf("traffic guard phases: guard=%s %s", trafficGuardName(run.scoped), strings.Join(breakdown, "; "))
	}
}

func trafficScalePercentile(values []time.Duration, percentile int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[min(len(sorted)-1, (len(sorted)*percentile+99)/100-1)].Round(time.Millisecond)
}
