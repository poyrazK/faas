// adr: 429 — binding/config changes and evidence expiry leave traffic untouched.
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingPromotionMem(t *testing.T) { bindingPromotionSuite(t, state.NewMemStore()) }
func TestBindingPromotionPG(t *testing.T) {
	store, _ := pgStore(t)
	bindingPromotionSuite(t, store)
}

func bindingPromotionFixture(t *testing.T, store state.Store) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@promotion.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "promotion-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	create := func(percent int) state.Deployment {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: "default", TrafficPercent: percent, TrafficPercentExplicit: true, ImageDigest: "sha256:" + strings.Repeat("1", 64)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "/test/"+dep.ID, "test/"+dep.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		dep, err = store.DeploymentByID(ctx, dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		return dep
	}
	return acct, app, create(100), create(0)
}

func bindingPromotionFence(t *testing.T, store state.BindingPromotionStore, acct state.Account, app state.App, target state.Deployment) state.BindingPromotionFence {
	t.Helper()
	revision, err := store.ReadBindingPromotionRevision(context.Background(), acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return state.BindingPromotionFence{AccountID: acct.ID, AppID: app.ID, DeploymentID: target.ID, Scope: "default", Revision: revision, ValidUntil: time.Now().Add(time.Minute)}
}

func bindingPromotionSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	acct, app, serving, candidate := bindingPromotionFixture(t, store)
	guarded := store.(state.BindingPromotionStore)
	unchanged := func() {
		t.Helper()
		for id, percent := range map[string]int{serving.ID: 100, candidate.ID: 0} {
			dep, err := store.DeploymentByID(ctx, id)
			if err != nil || dep.TrafficPercent != percent {
				t.Fatalf("traffic mutated: %+v %v", dep, err)
			}
		}
	}
	stale := bindingPromotionFence(t, guarded, acct, app, candidate)
	// Env updates can commit before the runtime-config timestamp is marked.
	if err := store.UpsertAppEnv(ctx, acct.ID, app.ID, "FEATURE", "changed"); err != nil {
		t.Fatal(err)
	}
	if _, err := guarded.PromoteDeploymentWithBindings(ctx, candidate.ID, stale, serving.ID); !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("accepted changed env: %v", err)
	}
	unchanged()
	fence := bindingPromotionFence(t, guarded, acct, app, candidate)
	fence.ValidUntil = time.Now().Add(-time.Second)
	if _, err := guarded.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !errors.Is(err, state.ErrBindingPromotionExpired) {
		t.Fatalf("accepted expired evidence: %v", err)
	}
	unchanged()
	fence = bindingPromotionFence(t, guarded, acct, app, candidate)
	if _, err := guarded.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, uuid.NewString()); !errors.Is(err, state.ErrTrafficServingChanged) {
		t.Fatalf("ignored serving expectation: %v", err)
	}
	unchanged()
	result, err := guarded.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID)
	if err != nil || result.FromPercent != 0 || result.Deployment.TrafficPercent != 100 || result.CheckedAt.IsZero() {
		t.Fatalf("promotion: %+v %v", result, err)
	}
	previous, _ := store.DeploymentByID(ctx, serving.ID)
	if previous.TrafficPercent != 0 {
		t.Fatalf("unbalanced traffic: %+v", previous)
	}
	// An idempotent retry still checks current facts and expiry, but no longer
	// expects the former deployment to serve 100%.
	fence = bindingPromotionFence(t, guarded, acct, app, candidate)
	result, err = guarded.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID)
	if err != nil || result.FromPercent != 100 {
		t.Fatalf("checked retry: %+v %v", result, err)
	}
	fence.ValidUntil = time.Now().Add(-time.Second)
	if _, err := guarded.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !errors.Is(err, state.ErrBindingPromotionExpired) {
		t.Fatalf("retry bypassed check: %v", err)
	}
}

func TestBindingPromotionPGWaitsForConfigCommit(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	acct, app, serving, candidate := bindingPromotionFixture(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fence := bindingPromotionFence(t, store, acct, app, candidate)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO app_envs(account_id,app_id,key,value) VALUES($1,$2,'FEATURE','rotated')`, acct.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("promotion did not wait for config transaction: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("accepted concurrently committed config: %v", err)
	}
	dep, _ := store.DeploymentByID(ctx, candidate.ID)
	if dep.TrafficPercent != 0 {
		t.Fatalf("candidate mutated: %+v", dep)
	}
}

func TestBindingPromotionPGRechecksExpiryAfterLockWait(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	acct, app, serving, candidate := bindingPromotionFixture(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fence := bindingPromotionFence(t, store, acct, app, candidate)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT app_id FROM app_binding_promotion_revisions WHERE app_id=$1 FOR UPDATE`, app.ID); err != nil {
		t.Fatal(err)
	}
	fence.ValidUntil = time.Now().Add(50 * time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, err := store.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("promotion did not wait: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrBindingPromotionExpired) {
		t.Fatalf("accepted expired-after-wait evidence: %v", err)
	}
	dep, _ := store.DeploymentByID(ctx, candidate.ID)
	if dep.TrafficPercent != 0 {
		t.Fatalf("candidate mutated: %+v", dep)
	}
}
