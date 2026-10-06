//go:build !no_pg

// adr: 623 — PostgreSQL promotion leases and config fences match the memory contract.
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingProjectPromotionPG(t *testing.T) {
	store, _, _ := pgWithPool(t)
	bindingProjectPromotionSuite(t, store)
}

func TestBindingProjectPromotionPGLeaseExpiryAfterLockWait(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	a, p, apps, members, previous := checkedProjectReleaseFixture(t, store)
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: p.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	var workloads []state.ProjectEnvironmentPromotionWorkload
	for i, member := range members {
		workloads = append(workloads, state.ProjectEnvironmentPromotionWorkload{WorkloadSlug: apps[i].Slug, WorkloadName: apps[i].WorkloadName, TargetDeploymentID: member.DeploymentID, Status: "unchanged"})
	}
	promotion, _, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{AccountID: a.ID, ProjectID: p.ID, ProjectSlug: p.Slug, FromEnvironment: "staging", ToEnvironment: "production", Status: "running", PromotionHash: api.EmptyProjectEnvironmentConfigHash(), IdempotencyKey: "expiry-promotion", ReleaseGraphMode: true, BindingsRequired: true, ReleaseTTLSeconds: 1800, PreviousTargetReleaseSetID: previous.ID}, workloads)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimBindingProjectPromotion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claim.Until = time.Now().UTC().Add(400 * time.Millisecond).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, "UPDATE project_environment_promotions SET binding_worker_until=$2 WHERE id=$1", promotion.ID, claim.Until); err != nil {
		t.Fatal(err)
	}
	fences := graphFences(ctx, t, store, a, members)
	report := api.ProjectReleaseCheckResponse{ProjectID: p.ID, Environment: "production", TTLSeconds: 1800, ExpectedActiveReleaseID: previous.ID, Passed: true, CheckedAt: time.Now().UTC()}
	for i, m := range members {
		report.Members = append(report.Members, api.ProjectReleaseSetMemberResponse{AppID: m.AppID, DeploymentID: m.DeploymentID})
		report.Checks = append(report.Checks, api.BindingCheckReport{App: apps[i].Slug, Scope: "production", DeploymentID: m.DeploymentID, Passed: true})
	}
	report.GraphDigest = api.ProjectReleaseGraphDigest(report)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT 1 FROM app_binding_promotion_revisions WHERE app_id=$1 FOR UPDATE", fences[0].AppID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.PublishBindingCheckedEnvironmentPromotion(state.WithBindingReleaseFences(ctx, fences), claim, 1800, members, report)
		done <- err
	}()
	time.Sleep(550 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, state.ErrBindingProjectPromotionLease) {
			t.Fatalf("expired worker activated: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("publish did not finish")
	}
	active, err := store.ActiveProjectReleaseSet(ctx, a.ID, p.ID, "production")
	if err != nil || active.ID != previous.ID {
		t.Fatalf("lease expiry changed graph: %+v %v", active, err)
	}
	var audits int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE kind='project.environment.promoted' AND data->>'promotion_id'=$1", promotion.ID).Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("failed transaction left audit: %d %v", audits, err)
	}
	// A replacement worker keeps the operation; the expired nonce cannot claim it.
	next, err := store.ClaimBindingProjectPromotion(ctx)
	if err != nil || next.PromotionID != claim.PromotionID || next.Token == claim.Token {
		t.Fatalf("lease recovery: %+v %v", next, err)
	}
}
