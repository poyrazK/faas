//go:build !no_pg

// adr: 20261027163626 — the scoped binding guard reaches the fleet-wide
// guard's verdicts, runs unrelated writes together, and refuses what it
// cannot verify.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
)

func scopedTrafficStore(pool *pgxpool.Pool, options ...StoreOption) *PgStore {
	return NewPgStore(pool, append([]StoreOption{WithTrafficScopedBindingGuard(true)}, options...)...)
}

// The binding writer suites assert the fleet-wide guard's accept and refuse
// verdicts; with the scoped guard every one must reach the same verdict.
func TestPgTrafficScopedBindingMatchesGlobalVerdicts(t *testing.T) {
	t.Run("app withdrawals", func(t *testing.T) {
		for _, mode := range trafficAppWithdrawalModes {
			t.Run(mode, func(t *testing.T) {
				_, pool, account, app := trafficHostPGFixture(t)
				testTrafficAppWithdrawal(t, scopedTrafficStore(pool), account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAppBindingIntent(t, pool) })
			})
		}
	})
	t.Run("app rename", func(t *testing.T) {
		_, pool, account, app := trafficHostPGFixture(t)
		testTrafficAppRenamePublication(t, scopedTrafficStore(pool, WithTrafficAppsDomain(".apps.example.test")), account, app, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAppBindingIntent(t, pool) })
	})
	t.Run("app purge reservations", func(t *testing.T) {
		for _, kind := range []string{"domain", "deleted-tenant"} {
			t.Run(kind, func(t *testing.T) {
				_, pool, account, app := trafficHostPGFixture(t)
				testTrafficAppPurgeGlobalReservation(t, scopedTrafficStore(pool), account, app, kind, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAppBindingIntent(t, pool) })
			})
		}
	})
	t.Run("alias withdrawal", func(t *testing.T) {
		for _, mode := range []string{"alias", "account", "purge-app"} {
			t.Run(mode, func(t *testing.T) {
				_, pool, account, app := trafficHostPGFixture(t)
				store := scopedTrafficStore(pool, WithTrafficAppsDomain("apps.example.test"))
				testTrafficAliasWithdrawal(t, store, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAccountIntent(t, pool) })
			})
		}
	})
	t.Run("alias revival", func(t *testing.T) {
		registerTrafficRevivalCapture(t)
		for _, mode := range trafficAliasRevivalModes {
			t.Run(mode, func(t *testing.T) {
				_, pool, account, app := trafficHostPGFixture(t)
				store := scopedTrafficStore(pool, WithTrafficAppsDomain("apps.example.test"))
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
					    'activity',(SELECT count(*) FROM org_activity_outbox))::text`, app.ID).Scan(&result)
					if err != nil {
						t.Fatal(err)
					}
					return result
				})
			})
		}
	})
	t.Run("rollback revival", func(t *testing.T) {
		for _, mode := range trafficRollbackRevivalModes {
			t.Run(mode, func(t *testing.T) {
				_, pool, account, app := trafficHostPGFixture(t)
				testTrafficRollbackRevival(t, scopedTrafficStore(pool), account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficRollbackIntent(t, pool) })
			})
		}
	})
	t.Run("tenant transitions", func(t *testing.T) {
		for _, mode := range trafficTenantRemovalModes {
			t.Run(mode, func(t *testing.T) {
				_, pool, account, app := trafficHostPGFixture(t)
				testTrafficTenantRemoval(t, scopedTrafficStore(pool), account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficTenantIntent(t, pool) })
			})
		}
	})
	t.Run("account retirement", func(t *testing.T) {
		for _, mode := range []string{"tenant", "domain", "global", "redirect"} {
			t.Run(mode, func(t *testing.T) {
				_, pool, account, app := trafficHostPGFixture(t)
				testTrafficAccountRetirement(t, scopedTrafficStore(pool), account, app, mode, func(host, id string) {
					if _, err := pool.Exec(t.Context(), `UPDATE custom_domains SET app_id_redirect=$2::uuid WHERE domain=$1::citext`, host, id); err != nil {
						t.Fatal(err)
					}
				}, func(rule EdgeRule) { seedPgTrafficAccountRule(t, pool, rule) }, func() string { return pgTrafficAccountIntent(t, pool) })
			})
		}
	})
}

// Binding writes for unrelated apps of different accounts run together under
// the scoped guard; the fleet-wide guard makes the second wait for the first.
func TestPgTrafficScopedBindingWritesRunConcurrently(t *testing.T) {
	store, pool, first, firstApp := trafficHostPGFixture(t)
	second, err := store.CreateAccount(t.Context(), "scoped-concurrency@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	secondApp, err := store.CreateApp(t.Context(), App{AccountID: second.ID, Slug: "scoped-concurrency", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, scoped := range []bool{true, false} {
		guarded := NewPgStore(pool, WithTrafficScopedBindingGuard(scoped))
		held, err := guarded.beginAccountAppTrafficMutation(t.Context(), first.ID, firstApp.ID)
		if err != nil {
			t.Fatal(err)
		}
		waiting, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		other, err := guarded.beginAccountAppTrafficMutation(waiting, second.ID, secondApp.ID)
		cancel()
		if other != nil {
			_ = other.Rollback(context.WithoutCancel(t.Context()))
		}
		_ = held.Rollback(context.WithoutCancel(t.Context()))
		if concurrent := err == nil; concurrent != scoped {
			t.Fatalf("scoped=%v: second write began=%v (err=%v)", scoped, concurrent, err)
		}
	}
}

// A scoped write that changes a hostname it did not declare is refused: its
// analysis never read or locked that hostname. The fleet-wide guard accepts
// the same write, and both report the miss.
func TestPgTrafficScopedBindingRefusesUndeclaredChange(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	other, err := store.CreateAccount(t.Context(), "undeclared@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	var missed []string
	SetTrafficBindingScopeObserver(func(_ string, kinds []string) { missed = append(missed, kinds...) })
	t.Cleanup(func() { SetTrafficBindingScopeObserver(nil) })
	for _, scoped := range []bool{false, true} {
		foreign, err := store.CreateApp(t.Context(), App{AccountID: other.ID, Slug: "undeclared-" + map[bool]string{false: "global", true: "scoped"}[scoped], Status: AppActive})
		if err != nil {
			t.Fatal(err)
		}
		missed = nil
		tx, err := NewPgStore(pool, WithTrafficScopedBindingGuard(scoped)).beginAccountAppTrafficMutation(t.Context(), account.ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		// Releasing another account's primary URL is outside this write's scope.
		if _, err := tx.Exec(t.Context(), `UPDATE apps SET slug=slug||'-moved' WHERE id=$1::uuid`, foreign.ID); err != nil {
			t.Fatal(err)
		}
		err = tx.Commit(t.Context())
		_ = tx.Rollback(context.WithoutCancel(t.Context()))
		var analysis *TrafficPolicyAnalysisError
		refused := errors.As(err, &analysis) && analysis.Scope == "binding_scope"
		if refused != scoped || !scoped && err != nil {
			t.Fatalf("scoped=%v: commit err=%v", scoped, err)
		}
		if len(missed) != 1 || missed[0] != TrafficBindingScopeKindReservationPrimary {
			t.Fatalf("scoped=%v: missed=%v", scoped, missed)
		}
	}
}

// The custom-domain writer suites assert the fleet-wide guard's verdicts;
// with the scoped guard every one must reach the same verdict.
func TestPgTrafficScopedDomainMatchesGlobalVerdicts(t *testing.T) {
	t.Run("publication", func(t *testing.T) {
		for _, form := range trafficDomainPublicationForms {
			t.Run(form, func(t *testing.T) {
				_, pool, account, _, app, environment := trafficEnvironmentPGFixture(t)
				store := scopedTrafficStore(pool)
				peerAccount, peer := globalTrafficPeer(t, store, "publication-peer")
				testTrafficDomainPublicationShadowAndActivation(t, store, account, app, environment, peerAccount, peer, form,
					func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() int { return publicationPgOutboxCount(t, pool) })
			})
		}
	})
	t.Run("foreign shadow", func(t *testing.T) {
		for _, mode := range []string{"exact-verified", "exact-unverified", "wildcard-verified", "wildcard-unverified"} {
			t.Run(mode, func(t *testing.T) {
				_, pool, account, app := trafficHostPGFixture(t)
				store := scopedTrafficStore(pool)
				_, peer := globalTrafficPeer(t, store, "publication-peer")
				testTrafficDomainPublicationRetainsForeignShadow(t, store, account, app, peer, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) })
			})
		}
	})
	t.Run("removal", func(t *testing.T) {
		for _, mode := range trafficDomainRemovalModes {
			for _, form := range trafficDomainRemovalForms {
				t.Run(mode+"/"+form, func(t *testing.T) {
					_, pool, _, app := trafficHostPGFixture(t)
					store := scopedTrafficStore(pool)
					peerAccount, peer := globalTrafficPeer(t, store, "removal-peer")
					testTrafficDomainRemoval(t, store, app, peerAccount, peer, mode, form,
						func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() int {
							var count int
							if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM org_activity_outbox`).Scan(&count); err != nil {
								t.Fatal(err)
							}
							return count
						})
				})
			}
		}
	})
	t.Run("verification", func(t *testing.T) {
		for _, mode := range trafficDomainVerificationModes {
			t.Run(mode, func(t *testing.T) {
				_, pool, account, app := trafficHostPGFixture(t)
				testTrafficDomainVerification(t, scopedTrafficStore(pool), account, app, mode, func(owner Account, source App, pattern string) {
					in := globalTrafficRule(owner, source, pattern, 520)
					encoded, err := json.Marshal(in.Action)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES('00000000-0000-0000-0000-000000000377',$1,$2,$3,'/',true,'route',$4::jsonb)`, owner.ID, source.ID, pattern, encoded); err != nil {
						t.Fatal(err)
					}
				})
			})
		}
	})
	t.Run("environment verification", func(t *testing.T) {
		for _, mode := range trafficScopedDomainModes {
			t.Run(mode, func(t *testing.T) {
				_, pool, account, project, app, environment := trafficEnvironmentPGFixture(t)
				testTrafficScopedDomainVerification(t, scopedTrafficStore(pool), account, project, app, environment, mode, func(rule EdgeRule) {
					seedPgScopedDomainLegacyRule(t, pool, rule)
				})
			})
		}
	})
	t.Run("environment overlay", func(t *testing.T) {
		_, pool, account, project, app, environment := trafficEnvironmentPGFixture(t)
		testTrafficScopedDomainOverlayMutation(t, scopedTrafficStore(pool), account, project, app, environment, func(rule EdgeRule) { seedPgScopedDomainLegacyRule(t, pool, rule) })
	})
}

// Domain publications for unrelated hostnames of different accounts run
// together under the scoped guard; the fleet-wide guard serializes them.
func TestPgTrafficScopedDomainWritesRunConcurrently(t *testing.T) {
	store, pool, _, firstApp := trafficHostPGFixture(t)
	_, secondApp := globalTrafficPeer(t, store, "scoped-domain-concurrency")
	for _, scoped := range []bool{true, false} {
		guarded := NewPgStore(pool, WithTrafficScopedBindingGuard(scoped))
		held, err := guarded.beginTrafficDomainPublication(t.Context(), "first.example.test", firstApp.ID)
		if err != nil {
			t.Fatal(err)
		}
		waiting, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		other, err := guarded.beginTrafficDomainPublication(waiting, "second.example.org", secondApp.ID)
		cancel()
		if other != nil {
			_ = other.Rollback(context.WithoutCancel(t.Context()))
		}
		_ = held.Rollback(context.WithoutCancel(t.Context()))
		if concurrent := err == nil; concurrent != scoped {
			t.Fatalf("scoped=%v: second publication began=%v (err=%v)", scoped, concurrent, err)
		}
	}
}

// A scoped domain write that changes a hostname it did not declare is
// refused; the fleet-wide guard accepts it, and both report the miss.
func TestPgTrafficScopedDomainRefusesUndeclaredChange(t *testing.T) {
	store, pool, _, app := trafficHostPGFixture(t)
	other, err := store.CreateAccount(t.Context(), "undeclared-domain@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	var missed []string
	SetTrafficBindingScopeObserver(func(_ string, kinds []string) { missed = append(missed, kinds...) })
	t.Cleanup(func() { SetTrafficBindingScopeObserver(nil) })
	for _, scoped := range []bool{false, true} {
		name := map[bool]string{false: "global", true: "scoped"}[scoped]
		foreign, err := store.CreateApp(t.Context(), App{AccountID: other.ID, Slug: "undeclared-domain-" + name, Status: AppActive})
		if err != nil {
			t.Fatal(err)
		}
		missed = nil
		tx, err := NewPgStore(pool, WithTrafficScopedBindingGuard(scoped)).beginTrafficDomainPublication(t.Context(), name+".example.test", app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `INSERT INTO custom_domains(domain,app_id,challenge_token) VALUES($1,$2,'token')`, name+".example.test", app.ID); err != nil {
			t.Fatal(err)
		}
		// Releasing another account's primary URL is outside this write's scope.
		if _, err := tx.Exec(t.Context(), `UPDATE apps SET slug=slug||'-moved' WHERE id=$1::uuid`, foreign.ID); err != nil {
			t.Fatal(err)
		}
		err = tx.Commit(t.Context())
		_ = tx.Rollback(context.WithoutCancel(t.Context()))
		var analysis *TrafficPolicyAnalysisError
		refused := errors.As(err, &analysis) && analysis.Scope == "binding_scope"
		if refused != scoped || !scoped && err != nil {
			t.Fatalf("scoped=%v: commit err=%v", scoped, err)
		}
		if len(missed) != 1 || missed[0] != TrafficBindingScopeKindReservationPrimary {
			t.Fatalf("scoped=%v: missed=%v", scoped, missed)
		}
	}
}
