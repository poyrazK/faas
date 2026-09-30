//go:build !no_pg

// adr: 375
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgTrafficEdgeRuleProjectionWriteRecovery(t *testing.T) {
	store, _ := pgStore(t)
	trafficRuleProjectionWriteRecovery(t, store)
}

func TestPgTrafficEdgeRuleProjectionLegacyRefusalAndEventRollback(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, _, app := trafficProjectionOwner(t, store)
	rule, err := store.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(account.ID, app.ID, "legacy-rule.example.test"))
	if err != nil {
		t.Fatal(err)
	}
	action, err := json.Marshal(largeTrafficRuleAction())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE edge_rules SET action=$2::jsonb WHERE id=$1`, rule.ID, action); err != nil {
		t.Fatal(err)
	}
	id := pgtype.UUID{Bytes: uuid.MustParse(rule.ID), Valid: true}
	row, err := sqlc.New().ReadBoundedTrafficEdgeRule(ctx, pool, sqlc.ReadBoundedTrafficEdgeRuleParams{RuleID: id, MaxBytes: api.TrafficPolicyMaxHostBytes})
	if err != nil || row.Observed <= api.TrafficPolicyMaxHostBytes || len(row.Data) != 0 {
		t.Fatalf("oversized row transferred: observed=%d bytes=%d err=%v", row.Observed, len(row.Data), err)
	}
	before, err := store.LatestEdgeRuleChangeID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	priority := 25
	_, err = store.UpdateEdgeRule(ctx, rule.ID, state.UpdateEdgeRuleParams{Priority: &priority})
	requireTrafficRuleProjectionError(t, err)
	after, err := store.LatestEdgeRuleChangeID(ctx)
	if err != nil || after != before {
		t.Fatalf("refused intent emitted change: before=%d after=%d err=%v", before, after, err)
	}
	small := state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: "repaired"}}
	if _, err := store.UpdateEdgeRule(ctx, rule.ID, state.UpdateEdgeRuleParams{Action: &small}); err != nil {
		t.Fatal(err)
	}
	measured, err := sqlc.New().MeasureEdgeRuleTrafficProjection(ctx, pool, id)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := sqlc.New().ReadPublicHostEdgeRules(ctx, pool, sqlc.ReadPublicHostEdgeRulesParams{
		Host: rule.MatchHost, AccountID: pgtype.UUID{Bytes: uuid.MustParse(account.ID), Valid: true},
		MaxRows: api.TrafficPolicyMaxHostRules, MaxBytes: api.TrafficPolicyMaxHostBytes})
	if err != nil || runtime.Oversized || measured != int64(len(runtime.Data)) {
		t.Fatalf("write/runtime projection differs: measured=%d runtime=%d err=%v", measured, len(runtime.Data), err)
	}
}

func TestPgTrafficPolicyMutationsShareAccountLockBeforeAppLock(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, project, app := trafficProjectionOwner(t, store)
	peerAccount, err := store.CreateAccount(ctx, "independent-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.CreateApp(ctx, state.App{AccountID: peerAccount.ID, Slug: "independent-policy", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "other-projection", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(account.ID, app.ID, "locked.example.test"))
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(account.Plan)
	preset, err := store.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: account.ID, Name: "locked",
		AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: other.ID, Kind: state.DeploymentKindImage, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := store.CreateCustomDomain(ctx, "locked-domain.example.test", other.ID, "current-token")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	owner := pgtype.UUID{Bytes: uuid.MustParse(account.ID), Valid: true}
	if _, err := sqlc.New().LockTrafficPolicyAccount(ctx, lock, owner); err != nil {
		t.Fatal(err)
	}
	priority := 25
	for _, mutation := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"rule-create", func(ctx context.Context) error {
			_, err := store.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(account.ID, other.ID, "locked-other.example.test"))
			return err
		}},
		{"quota-rule-create", func(ctx context.Context) error {
			_, err := store.CreateEdgeRuleIfUnderQuota(ctx, pgSampleEdgeRuleParams(account.ID, other.ID, "locked-quota.example.test"), limits)
			return err
		}},
		{"rule-update", func(ctx context.Context) error {
			_, err := store.UpdateEdgeRule(ctx, rule.ID, state.UpdateEdgeRuleParams{Priority: &priority})
			return err
		}},
		{"preset-create", func(ctx context.Context) error {
			_, err := store.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: account.ID, AppID: other.ID, Name: "locked-other", AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}}, limits)
			return err
		}},
		{"preset-update", func(ctx context.Context) error {
			_, err := store.UpdateCorsPreset(ctx, account.ID, preset.ID, preset)
			return err
		}},
		{"environment-overlay", func(ctx context.Context) error {
			_, err := store.PutProjectEnvironmentEdgePolicy(ctx, state.ProjectEnvironmentEdgePolicy{AccountID: account.ID,
				ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production"})
			return err
		}},
		{"environment-clone", func(ctx context.Context) error {
			_, _, err := store.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: account.ID,
				ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging"}, limits)
			return err
		}},
		{"alias-publication", func(ctx context.Context) error {
			_, err := store.SetDeploymentAlias(ctx, other.ID, "candidate", deployment.ID)
			return err
		}},
		{"deployment-status", func(ctx context.Context) error {
			return store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployImaging, "")
		}},
		{"deployment-mark-live", func(ctx context.Context) error {
			return store.MarkDeploymentLive(ctx, deployment.ID)
		}},
		{"domain-verification", func(ctx context.Context) error {
			return store.MarkDomainVerified(ctx, domain.Domain)
		}},
		{"domain-challenge-verification", func(ctx context.Context) error {
			_, err := store.MarkDomainVerifiedIfChallenge(ctx, domain.Domain, domain.ChallengeToken)
			return err
		}},
		{"domain-quota-claim", func(ctx context.Context) error {
			_, err := store.CreateCustomDomainIfUnderQuota(ctx, "locked-claim.example.test", other.ID, "token", 10, 20)
			return err
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			bounded, cancel := context.WithTimeout(ctx, 75*time.Millisecond)
			defer cancel()
			if err := mutation.run(bounded); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("mutation bypassed account lock: %v", err)
			}
			// A waiting quota writer must not already hold the app lock.
			if _, err := pool.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE NOWAIT`, other.ID); err != nil {
				t.Fatalf("account waiter retained an app lock: %v", err)
			}
		})
	}
	aliases, err := store.ListDeploymentAliases(ctx, other.ID)
	if err != nil || len(aliases) != 0 {
		t.Fatalf("canceled alias publication changed intent: %+v err=%v", aliases, err)
	}
	current, err := store.DeploymentByID(ctx, deployment.ID)
	if err != nil || current.Status != deployment.Status {
		t.Fatalf("canceled deployment writer changed status: %s err=%v", current.Status, err)
	}
	if got, err := store.DomainByName(ctx, domain.Domain); err != nil || got.Verified() {
		t.Fatalf("canceled verification changed domain: %+v err=%v", got, err)
	}
	independent, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err := store.CreateEdgeRule(independent, pgSampleEdgeRuleParams(peerAccount.ID, peer.ID, "independent-policy.example.test")); err != nil {
		t.Fatalf("one account's lock blocked another account: %v", err)
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateEdgeRule(ctx, rule.ID, state.UpdateEdgeRuleParams{Priority: &priority}); err != nil {
		t.Fatalf("cancelled waiter leaked account lock: %v", err)
	}
	if _, err := store.SetDeploymentAlias(ctx, other.ID, "candidate", deployment.ID); err != nil {
		t.Fatalf("alias publication after canceled waiter: %v", err)
	}
	if err := store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployImaging, ""); err != nil {
		t.Fatalf("deployment status after canceled waiter: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatalf("mark live after canceled waiter: %v", err)
	}
	if matched, err := store.MarkDomainVerifiedIfChallenge(ctx, domain.Domain, domain.ChallengeToken); err != nil || !matched {
		t.Fatalf("domain verification after canceled waiter: matched=%v err=%v", matched, err)
	}
	if _, err := store.CreateCustomDomainIfUnderQuota(ctx, "locked-claim.example.test", other.ID, "token", 10, 20); err != nil {
		t.Fatalf("domain quota claim after canceled waiter: %v", err)
	}
}

func TestPgTrafficPresetAccountQuotaSerializesDifferentApps(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, _, app := trafficProjectionOwner(t, store)
	other, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "other-quota", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(account.Plan)
	limits.CorsPresetsPerAccount = 1
	start, results := make(chan struct{}), make(chan error, 2)
	for _, candidate := range []state.App{app, other} {
		go func() {
			<-start
			_, err := store.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: account.ID, AppID: candidate.ID,
				Name: "last-slot", AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}}, limits)
			results <- err
		}()
	}
	close(start)
	accepted, refused := 0, 0
	for range 2 {
		err := <-results
		var quota *state.CorsPresetQuotaError
		switch {
		case err == nil:
			accepted++
		case errors.As(err, &quota) && quota.Scope == state.CorsPresetQuotaScopeAccount:
			refused++
		default:
			t.Fatalf("unexpected competing write result: %v", err)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatalf("account quota raced: accepted=%d refused=%d", accepted, refused)
	}
}

func TestPgTrafficPolicyAccountWaitersDoNotExhaustPool(t *testing.T) {
	base, source, ctx := pgStoreWithPool(t)
	account, _, app := trafficProjectionOwner(t, base)
	config := source.Config()
	config.MaxConns = 3
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lock, err := pool.Begin(bounded)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := sqlc.New().LockTrafficPolicyAccount(bounded, lock, pgtype.UUID{Bytes: uuid.MustParse(account.ID), Valid: true}); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	const contenders = 6
	results := make(chan error, contenders)
	var started sync.WaitGroup
	started.Add(contenders)
	for range contenders {
		go func() {
			started.Done()
			_, err := store.CreateEdgeRule(bounded, pgSampleEdgeRuleParams(account.ID, app.ID, "contended.example.test"))
			results <- err
		}()
	}
	started.Wait()
	time.Sleep(150 * time.Millisecond)
	read, stopRead := context.WithTimeout(bounded, 200*time.Millisecond)
	defer stopRead()
	var one int
	if err := pool.QueryRow(read, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("account waiters exhausted ordinary read capacity: one=%d err=%v", one, err)
	}
	if err := lock.Rollback(bounded); err != nil {
		t.Fatal(err)
	}
	for range contenders {
		if err := <-results; err != nil {
			t.Fatalf("contender after release: %v", err)
		}
	}
}
