package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_InvocationScopeAdoptionDoesNotReroutePendingWork(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	old, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
		Source: state.InvocationDelayedTask, Method: "POST", Path: "/task", DueAt: time.Now().Add(time.Hour)})
	if err != nil || old.DeploymentScope != "default" {
		t.Fatalf("standalone admission: scope=%q, err=%v", old.DeploymentScope, err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: accountID, Slug: "adopt-invocation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET project_id = $2 WHERE id = $1`, appID, project.ID); err != nil {
		t.Fatal(err)
	}
	newer, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
		Source: state.InvocationDelayedTask, Method: "POST", Path: "/task", DueAt: time.Now().Add(time.Hour)})
	if err != nil || newer.DeploymentScope != "production" {
		t.Fatalf("adopted admission: scope=%q, err=%v", newer.DeploymentScope, err)
	}
	got, err := store.InvocationByID(ctx, old.ID)
	if err != nil || got.DeploymentScope != "default" {
		t.Fatalf("adoption rewrote queued work: scope=%q, err=%v", got.DeploymentScope, err)
	}
	// Before the project has a release graph, legacy deployment selection is
	// still allowed. It must retain the admitted scope rather than production.
	forwarded, version, err := state.ResolveInvocationVersion(ctx, store, got)
	if err != nil || forwarded.DeploymentScope != "default" || version.Scope != "default" {
		t.Fatalf("queued default scope silently rerouted: %+v, %v", version, err)
	}
}
