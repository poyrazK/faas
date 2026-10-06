//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCheckedProjectReleasePG(t *testing.T) {
	store, _, _ := pgWithPool(t)
	checkedProjectReleaseSuite(t, store)
}

func TestCheckedProjectReleasePGConcurrentActivation(t *testing.T) {
	store, ctx, _ := pgWithPool(t)
	acct, project, _, members, old := checkedProjectReleaseFixture(t, store)
	fences := graphFences(t, store, acct, members)
	start, results := make(chan struct{}), make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := store.PublishProjectReleaseSetWithBindings(state.WithBindingReleaseFences(ctx, fences), acct.ID, project.ID, "production", old.ID, 1800, members)
			results <- err
		}()
	}
	close(start)
	success, conflict := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			success++
		case errors.Is(err, state.ErrConflict):
			conflict++
		default:
			t.Fatalf("unexpected publication result: %v", err)
		}
	}
	rows, err := store.ListProjectReleaseSetsBefore(ctx, acct.ID, project.ID, "production", time.Time{}, "", 10)
	if err != nil || success != 1 || conflict != 1 || len(rows) != 2 {
		t.Fatalf("concurrent graph CAS: success=%d conflict=%d rows=%d err=%v", success, conflict, len(rows), err)
	}
}

func TestCheckedProjectReleasePGExpiryAfterLockWait(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	acct, project, _, members, old := checkedProjectReleaseFixture(t, store)
	fences := graphFences(t, store, acct, members)
	fences[0].ValidUntil = time.Now().Add(350 * time.Millisecond)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT 1 FROM app_binding_promotion_revisions WHERE app_id=$1 FOR UPDATE", fences[1].AppID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.PublishProjectReleaseSetWithBindings(state.WithBindingReleaseFences(context.Background(), fences), acct.ID, project.ID, "production", old.ID, 1800, members)
		done <- err
	}()
	time.Sleep(500 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, state.ErrBindingPromotionExpired) {
			t.Fatalf("expired first member accepted: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("publication did not finish")
	}
	current, err := store.ActiveProjectReleaseSet(ctx, acct.ID, project.ID, "production")
	if err != nil || current.ID != old.ID {
		t.Fatalf("graph changed: %+v %v", current, err)
	}
}

func TestCheckedProjectReleasePGExactTaskAndGrantIsolation(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	acct, project, _, members, old := checkedProjectReleaseFixture(t, store)
	pin := &state.BindingVerificationPin{Type: "service", Binding: "billing", Revision: strings.Repeat("1", 64), TargetDeploymentID: members[1].DeploymentID}
	task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: acct.ID, AppID: members[0].AppID, DeploymentID: members[0].DeploymentID, RequireLiveDeployment: true, Kind: state.AppTaskKindManual, Command: []string{api.AppTaskServiceBindingProbeCommand, "billing", members[1].DeploymentID}, BindingVerification: pin, TimeoutSeconds: 15, MaxOutputBytes: 4096, CreatedAt: time.Now().UTC()})
	if err != nil || task.BindingVerification == nil || task.BindingVerification.TargetDeploymentID != members[1].DeploymentID {
		t.Fatalf("exact task: %+v %v", task, err)
	}
	fences := graphFences(t, store, acct, members)
	activated, err := store.PublishProjectReleaseSetWithBindings(state.WithBindingReleaseFences(ctx, fences), acct.ID, project.ID, "production", old.ID, 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE kind='project.release_set_checked' AND data->>'release_id'=$1", activated.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("durable audit: count=%d %v", audits, err)
	}
	// A committed transaction must not leave an authorization grant for a later write.
	if _, err := pool.Exec(ctx, "UPDATE project_release_sets SET active=false WHERE id=$1", activated.ID); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("grant escaped transaction: %v", err)
	}
}
