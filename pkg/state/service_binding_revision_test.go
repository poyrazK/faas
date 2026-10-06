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

func TestServiceBindingRevisionMem(t *testing.T) { serviceBindingRevisionSuite(t, state.NewMemStore()) }
func TestServiceBindingRevisionPG(t *testing.T) {
	s, _ := pgStore(t)
	serviceBindingRevisionSuite(t, s)
}

func serviceBindingRevisionSuite(t *testing.T, store state.Store) {
	ctx := context.Background()
	acct, app, _, _ := bindingPromotionFixture(t, store)
	manifest := state.AppManifest{ServiceBindings: api.ServiceBindingsForTargets([]string{"billing"})}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	reader := store.(state.ServiceBindingRevisionStore)
	read := func() string {
		t.Helper()
		r, err := reader.ReadServiceBindingRevision(ctx, acct.ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	before := read()
	if err := store.UpsertAppEnv(ctx, acct.ID, app.ID, "FEATURE", "value"); err != nil {
		t.Fatal(err)
	}
	if read() != before {
		t.Fatal("env mutation invalidated dependency token")
	}
	other, err := store.CreateAccount(ctx, uuid.NewString()+"@service-revision.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	create := func(account, slug string, expiry *time.Time) state.App {
		t.Helper()
		input := state.App{AccountID: account, Slug: slug, Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60, PreviewExpiresAt: expiry}
		if expiry != nil {
			input.PreviewOfSlug = "billing"
			input.PreviewPrState = state.PreviewPrStateOpen
		}
		a, err := store.CreateApp(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	create(other.ID, "foreign-"+uuid.NewString()[:8], nil)
	if read() != before {
		t.Fatal("other account invalidated service evidence")
	}
	if _, err := reader.ReadServiceBindingRevision(ctx, other.ID, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account revision read: %v", err)
	}
	target := create(acct.ID, "target-"+uuid.NewString()[:8], nil)
	changed := func() {
		t.Helper()
		after := read()
		if after == before {
			t.Fatal("dependency change retained revision")
		}
		before = after
	}
	changed()
	empty := []string{}
	targetManifest := state.AppManifest{AllowedServiceCallers: &empty}
	if _, err := store.UpdateApp(ctx, target.ID, state.UpdateAppParams{Manifest: &targetManifest}); err != nil {
		t.Fatal(err)
	}
	changed()
	if _, err := store.UpdateApp(ctx, target.ID, state.UpdateAppParams{Manifest: &targetManifest}); err != nil {
		t.Fatal(err)
	}
	if read() != before {
		t.Fatal("identical target configuration invalidated revision")
	}
	websocket := true
	if _, err := store.UpdateApp(ctx, target.ID, state.UpdateAppParams{WebSocketEnabled: &websocket, SetWebSocketEnabled: true}); err != nil {
		t.Fatal(err)
	}
	changed()
	policyStore := store.(state.GitHubDeployPolicyStore)
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "policy-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	policy := state.DefaultGitHubDeployPolicy(project.ID, acct.ID)
	if _, err := policyStore.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	changed()
	policy.RootDir = "src"
	if _, err := policyStore.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if read() != before {
		t.Fatal("build-only policy invalidated service evidence")
	}
	policy.PreviewServicePolicy = state.PreviewServicePolicyAllowMarked
	if _, err := policyStore.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	changed()
	expiry := time.Now().Add(time.Hour)
	preview := create(acct.ID, "preview-"+uuid.NewString()[:8], &expiry)
	changed()
	run := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := store.RegisterScenarioTestMembers(ctx, acct.ID, run, []state.ScenarioTestMember{{Workload: "billing", AppID: preview.ID}}); err != nil {
		t.Fatal(err)
	}
	changed()
	if _, err := store.SoftDeleteAppCascade(ctx, preview.ID); err != nil {
		t.Fatal(err)
	}
	changed()
	if err := store.DeleteScenarioTestMembers(ctx, acct.ID, run); err != nil {
		t.Fatal(err)
	}
	changed()
}

func TestBindingPromotionPGWaitsForServicePolicyCommit(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	acct, app, serving, candidate := bindingPromotionFixture(t, store)
	target, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "target-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.AppManifest{ServiceBindings: api.ServiceBindingsForTargets([]string{target.Slug})}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	fence := bindingPromotionFence(t, store, acct, app, candidate)
	revision, _ := store.ReadServiceBindingRevision(ctx, acct.ID, app.ID)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE apps SET manifest='{"allowed_service_callers":[]}' WHERE id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := store.ReadServiceBindingRevision(ctx, acct.ID, app.ID)
	if after != revision {
		t.Fatal("rollback invalidated service evidence")
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE apps SET manifest='{"allowed_service_callers":[]}' WHERE id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("promotion did not wait for target transaction: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("accepted concurrently changed target: %v", err)
	}
	dep, _ := store.DeploymentByID(ctx, candidate.ID)
	if dep.TrafficPercent != 0 {
		t.Fatalf("candidate promoted: %+v", dep)
	}
}
