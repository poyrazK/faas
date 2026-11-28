package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppTaskConcurrencyCapMem(t *testing.T) {
	appTaskConcurrencySuite(t, state.NewMemStore())
}

func TestAppTaskConcurrencyCapPG(t *testing.T) {
	store, _ := pgStore(t)
	appTaskConcurrencySuite(t, store)
}

func appTaskConcurrencySuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "app-task-cap@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanHobby)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID, Slug: "app-task-cap", Type: state.AppTypeApp,
		Runtime: "node22", RAMMB: limits.RAMMB, MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, Scope: "default", ImageDigest: "sha256:" + strings.Repeat("2", 64), CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/tmp/app-task-cap.ext4", "apps/app-task-cap/rootfs.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	create := func(command []string, limit int) (state.AppTask, error) {
		return store.CreateAppTask(ctx, state.CreateAppTaskParams{
			AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
			Kind: state.AppTaskKindManual, Command: command, TimeoutSeconds: 30, CreatedAt: now,
			CapActiveTasks: true, MaxActiveTasks: limit,
		})
	}

	// Concurrent submissions never overshoot the cap.
	const limit = 2
	var wg sync.WaitGroup
	results := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := create([]string{"bin/work"}, limit)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	admitted, refused := 0, 0
	for err := range results {
		var limitErr *state.AppTaskConcurrencyError
		switch {
		case err == nil:
			admitted++
		case errors.As(err, &limitErr) && errors.Is(err, state.ErrAppTaskConcurrencyLimit):
			if limitErr.Limit != limit || limitErr.Active != limit {
				t.Fatalf("limit error = %+v", limitErr)
			}
			refused++
		default:
			t.Fatalf("CreateAppTask: %v", err)
		}
	}
	if admitted != limit || refused != 3 {
		t.Fatalf("admitted %d refused %d, want %d and 3", admitted, refused, limit)
	}

	// Probes are neither capped nor counted, and an uncapped call is admitted.
	if _, err := create([]string{api.AppTaskMigrationDependenciesCommand}, limit); err != nil {
		t.Fatalf("probe at the cap: %v", err)
	}
	if _, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{
		AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
		Kind: state.AppTaskKindManual, Command: []string{"bin/work"}, TimeoutSeconds: 30, CreatedAt: now,
	}); err != nil {
		t.Fatalf("uncapped create: %v", err)
	}

	// Cancelling a queued task frees its slot; a cap of 0 refuses everything.
	tasks, err := store.ListAppTasks(ctx, account.ID, app.ID, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var cancelled int
	for _, task := range tasks {
		if task.Command[0] == "bin/work" && cancelled < 2 {
			if _, err := store.RequestAppTaskCancellation(ctx, account.ID, app.ID, task.ID, now); err != nil {
				t.Fatal(err)
			}
			cancelled++
		}
	}
	if _, err := create([]string{"bin/work"}, limit); err != nil {
		t.Fatalf("create after cancel: %v", err)
	}
	if _, err := create([]string{"bin/work"}, 0); !errors.Is(err, state.ErrAppTaskConcurrencyLimit) {
		t.Fatalf("cap 0: err = %v, want a concurrency refusal", err)
	}
}
