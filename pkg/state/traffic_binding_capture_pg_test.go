//go:build !no_pg

// adr: 20261029190349 — the capture triggers record every claim and
// reservation a write touches.
package state

import (
	"context"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPgTrafficBindingCaptureRecordsEverySource(t *testing.T) {
	const suffix = ".apps.example.test"
	_, pool, account, project, app, _ := trafficEnvironmentPGFixture(t)
	store := NewPgStore(pool, WithTrafficAppsDomain(suffix))
	ctx := t.Context()
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{AccountID: account.ID, AppID: app.ID, Name: "capture"}, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	var environment string
	if err := pool.QueryRow(ctx, `INSERT INTO project_environments(account_id,project_id,slug) VALUES ($1,$2,'capture') RETURNING id::text`, account.ID, project.ID).Scan(&environment); err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		statement string
		args      []any
	}{
		{`INSERT INTO custom_domains(domain,app_id,challenge_token,verified_at) VALUES ('seeded.example.org',$1,'token',now())`, []any{app.ID}},
		{`INSERT INTO custom_domains(domain,app_id,challenge_token,verified_at,environment_id) VALUES ('env.example.org',$1,'token',now(),$2::uuid)`, []any{app.ID, environment}},
		{`INSERT INTO tenant_hostnames(surface_id,hostname,challenge_token) VALUES ($1::uuid,'tenant.example.org','token')`, []any{surface.ID}},
		{`INSERT INTO deployment_aliases(app_id,name,deployment_id) VALUES ($1,'green',$2)`, []any{app.ID, deployment.ID}},
	} {
		if _, err := pool.Exec(ctx, seed.statement, seed.args...); err != nil {
			t.Fatalf("%s: %v", seed.statement, err)
		}
	}
	alias := func(name string) string { return "tag-" + name + "-" + strings.ReplaceAll(app.ID, "-", "") + suffix }
	primary := app.Slug + suffix
	appHosts := []string{primary, "seeded.example.org", "env.example.org", "tenant.example.org", alias("green")}
	for _, tc := range []struct {
		name, statement string
		args            []any
		want            []string // "+" marks an added exact host, "*" a subtree
	}{
		{"domain insert", `INSERT INTO custom_domains(domain,app_id,challenge_token) VALUES ('new.example.org',$1,'token')`, []any{app.ID}, []string{"+new.example.org"}},
		{"wildcard domain insert", `INSERT INTO custom_domains(domain,app_id,challenge_token) VALUES ('*.wild.example.org',$1,'token')`, []any{app.ID}, []string{"*wild.example.org"}},
		{"domain verification", `UPDATE custom_domains SET verified_at=NULL WHERE domain='seeded.example.org'`, nil, []string{"seeded.example.org"}},
		{"domain delete", `DELETE FROM custom_domains WHERE domain='seeded.example.org'`, nil, []string{"seeded.example.org"}},
		{"certificate update", `UPDATE custom_domains SET cert_status='issued' WHERE domain='seeded.example.org'`, nil, nil},
		{"tenant hostname insert", `INSERT INTO tenant_hostnames(surface_id,hostname,challenge_token) VALUES ($1::uuid,'more.example.org','token')`, []any{surface.ID}, []string{"+more.example.org"}},
		{"tenant hostname delete", `DELETE FROM tenant_hostnames WHERE hostname='tenant.example.org'`, nil, []string{"tenant.example.org"}},
		{"surface status", `UPDATE tenant_surfaces SET status='deleted' WHERE id=$1::uuid`, []any{surface.ID}, []string{"tenant.example.org"}},
		{"alias insert", `INSERT INTO deployment_aliases(app_id,name,deployment_id) VALUES ($1,'blue',$2)`, []any{app.ID, deployment.ID}, []string{"+" + alias("blue")}},
		{"alias delete", `DELETE FROM deployment_aliases WHERE app_id=$1 AND name='green'`, []any{app.ID}, []string{alias("green")}},
		{"app rename", `UPDATE apps SET slug='capture-renamed' WHERE id=$1`, []any{app.ID}, append([]string{"capture-renamed" + suffix}, appHosts...)},
		{"app visibility", `UPDATE apps SET visibility='internal' WHERE id=$1`, []any{app.ID}, appHosts},
		{"app no-op update", `UPDATE apps SET slug=slug WHERE id=$1`, []any{app.ID}, nil},
		{"environment delete", `DELETE FROM project_environments WHERE id=$1::uuid`, []any{environment}, []string{"env.example.org"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := tx.Exec(ctx, tc.statement, tc.args...); err != nil {
				t.Fatal(err)
			}
			captured, err := readTrafficBindingCapture(ctx, tx, suffix)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := trafficCaptureNames(captured), trafficCaptureNamesOf(tc.want); !slices.Equal(got, want) {
				t.Fatalf("captured %v, want %v", got, want)
			}
		})
	}
}

func trafficCaptureNames(changes []trafficBindingChange) []string {
	var names []string
	for _, change := range changes {
		name := change.host
		switch {
		case change.subtree:
			name = "*" + name
		case change.added:
			name = "+" + name
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return slices.Compact(names)
}

func trafficCaptureNamesOf(names []string) []string {
	names = slices.Clone(names)
	sort.Strings(names)
	return slices.Compact(names)
}

// Every table the claim and reservation queries read has the capture
// triggers, so no write to a claim or reservation source goes unrecorded.
func TestPgTrafficBindingCaptureCoversEveryClaimSource(t *testing.T) {
	raw, err := os.ReadFile("queries.sql")
	if err != nil {
		t.Fatal(err)
	}
	queries := string(raw)
	block := func(name string) string {
		start := strings.Index(queries, "-- name: "+name+" ")
		if start < 0 {
			t.Fatalf("query %s not found", name)
		}
		end := strings.Index(queries[start+1:], "-- name: ")
		return queries[start : start+1+end]
	}
	analysis := block("ReadTrafficHostAnalysis")
	start, end := strings.Index(analysis, "reservations AS ("), strings.Index(analysis, "), bounds AS (")
	if start < 0 || end < start {
		t.Fatal("reservations CTE not found in ReadTrafficHostAnalysis")
	}
	sources := map[string]bool{}
	ctes := regexp.MustCompile(`(?i)\b(\w+)\s+AS\s+\(`)
	tables := regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([a-z_]+)\b\s*([^(\s]|$)`)
	for _, sql := range []string{block("ReadTrafficBindingClaims"), analysis[start:end]} {
		local := map[string]bool{}
		for _, match := range ctes.FindAllStringSubmatch(sql, -1) {
			local[strings.ToLower(match[1])] = true
		}
		for _, match := range tables.FindAllStringSubmatch(sql, -1) {
			if name := strings.ToLower(match[1]); !local[name] {
				sources[name] = true
			}
		}
	}
	_, pool, _, _ := trafficHostPGFixture(t)
	rows, err := pool.Query(t.Context(), `SELECT DISTINCT c.relname FROM pg_trigger tg JOIN pg_class c ON c.oid = tg.tgrelid
		JOIN pg_proc p ON p.oid = tg.tgfoid WHERE p.proname = 'traffic_binding_capture' AND NOT tg.tgisinternal`)
	if err != nil {
		t.Fatal(err)
	}
	captured := map[string]bool{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		captured[table] = true
	}
	rows.Close()
	if len(sources) < 5 {
		t.Fatalf("parsed only %v from the claim queries", sources)
	}
	for source := range sources {
		if !captured[source] {
			t.Errorf("%s feeds a claim or reservation but has no capture trigger", source)
		}
	}
}

// The binding-writer suites run under the fleet-wide guard, whose commit
// check diffs the fleet; the capture must record every hostname that diff
// finds changed.
func TestPgTrafficBindingCaptureCoversWriterSuites(t *testing.T) {
	var mu sync.Mutex
	gaps := map[string][]string{}
	SetTrafficBindingCaptureObserver(func(writer string, missed []string) {
		if len(missed) > 0 {
			mu.Lock()
			gaps[writer] = append(gaps[writer], missed...)
			mu.Unlock()
		}
	})
	t.Cleanup(func() { SetTrafficBindingCaptureObserver(nil) })
	t.Run("app withdrawals", func(t *testing.T) {
		for _, mode := range trafficAppWithdrawalModes {
			t.Run(mode, func(t *testing.T) {
				store, pool, account, app := trafficHostPGFixture(t)
				testTrafficAppWithdrawal(t, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAppBindingIntent(t, pool) })
			})
		}
	})
	t.Run("tenant transitions", func(t *testing.T) {
		for _, mode := range trafficTenantRemovalModes {
			t.Run(mode, func(t *testing.T) {
				store, pool, account, app := trafficHostPGFixture(t)
				testTrafficTenantRemoval(t, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficTenantIntent(t, pool) })
			})
		}
	})
	t.Run("account retirement", func(t *testing.T) {
		for _, mode := range []string{"tenant", "domain", "global", "redirect"} {
			t.Run(mode, func(t *testing.T) {
				store, pool, account, app := trafficHostPGFixture(t)
				testTrafficAccountRetirement(t, store, account, app, mode, func(host, id string) {
					if _, err := pool.Exec(t.Context(), `UPDATE custom_domains SET app_id_redirect=$2::uuid WHERE domain=$1::citext`, host, id); err != nil {
						t.Fatal(err)
					}
				}, func(rule EdgeRule) { seedPgTrafficAccountRule(t, pool, rule) }, func() string { return pgTrafficAccountIntent(t, pool) })
			})
		}
	})
	t.Run("alias withdrawal", func(t *testing.T) {
		for _, mode := range []string{"alias", "account", "purge-app"} {
			t.Run(mode, func(t *testing.T) {
				_, pool, account, app := trafficHostPGFixture(t)
				store := NewPgStore(pool, WithTrafficAppsDomain("apps.example.test"))
				testTrafficAliasWithdrawal(t, store, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAccountIntent(t, pool) })
			})
		}
	})
	t.Run("domain removal", func(t *testing.T) {
		for _, mode := range trafficDomainRemovalModes {
			for _, form := range trafficDomainRemovalForms {
				t.Run(mode+"/"+form, func(t *testing.T) {
					store, pool, _, app := trafficHostPGFixture(t)
					peerAccount, peer := globalTrafficPeer(t, store, "removal-peer")
					testTrafficDomainRemoval(t, store, app, peerAccount, peer, mode, form,
						func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() int { return publicationPgOutboxCount(t, pool) })
				})
			}
		}
	})
	t.Run("domain publication", func(t *testing.T) {
		for _, form := range trafficDomainPublicationForms {
			t.Run(form, func(t *testing.T) {
				store, pool, account, _, app, environment := trafficEnvironmentPGFixture(t)
				peerAccount, peer := globalTrafficPeer(t, store, "publication-peer")
				testTrafficDomainPublicationShadowAndActivation(t, store, account, app, environment, peerAccount, peer, form,
					func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() int { return publicationPgOutboxCount(t, pool) })
			})
		}
	})
	mu.Lock()
	defer mu.Unlock()
	for writer, kinds := range gaps {
		t.Errorf("%s: capture missed changed hostnames of kinds %v", writer, kinds)
	}
}
