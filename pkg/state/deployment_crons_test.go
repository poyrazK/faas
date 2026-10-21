package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func cronStageFixture(t *testing.T, store state.Store) (state.App, state.DeploymentCronStagingParams, state.Cron) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "cron-journal@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "cron-journal"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/a", "/b"} {
		if _, err = store.CreateCron(ctx, app.ID, "* * * * *", path, true); err != nil {
			t.Fatal(err)
		}
	}
	command, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "", true, state.CronOptions{Command: []string{"echo", "independent"}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.(state.DeploymentCronStagingStore).DeploymentCronStagingSnapshot(ctx, acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	desired := []api.DeploymentCronConfig{snapshot.Crons[0], {ID: uuid.NewString(), Schedule: "0 * * * *", Path: "/new", Enabled: true, Timezone: "UTC"}}
	desired[0].Enabled = false
	desired[0].Timezone = "Europe/Istanbul"
	limits := api.MustLimitsFor(api.PlanPro)
	limits.CronLimitPerApp = 3
	limits.CronLimitPerAccount = 3
	return app, state.DeploymentCronStagingParams{AccountID: acct.ID, Limits: limits, Request: api.DeploymentCronStagingRequest{SessionID: strings.Repeat("a", 32), Operation: "stage", ExpectedAppID: app.ID, ExpectedDeploymentID: snapshot.LatestDeploymentID, ExpectedRevision: snapshot.Revision, Before: snapshot.Crons, Desired: desired}}, command
}
func TestDeploymentCronJournalMemory(t *testing.T) { testCronJournal(t, state.NewMemStore()) }
func testCronJournal(t *testing.T, store state.Store) {
	app, p, command := cronStageFixture(t, store)
	s := store.(state.DeploymentCronStagingStore)
	ctx := t.Context()
	old, err := store.CronByID(ctx, p.Request.Before[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := s.ApplyDeploymentCronStaging(ctx, app.ID, p)
	if err != nil || !staged.Changed {
		t.Fatalf("stage=%+v err=%v", staged, err)
	}
	for range 2 {
		duplicate, err := s.ApplyDeploymentCronStaging(ctx, app.ID, p)
		if err != nil || duplicate.Changed || duplicate.Receipt != staged.Receipt {
			t.Fatalf("duplicate=%+v err=%v", duplicate, err)
		}
	}
	after, err := s.DeploymentCronStagingSnapshot(ctx, p.AccountID, app.ID)
	if err != nil || !api.DeploymentCronsEqual(after.Crons, p.Request.Desired) {
		t.Fatalf("snapshot=%+v err=%v", after, err)
	}
	// Bookkeeping and command-cron edits do not steal HTTP ownership.
	fired := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
	if err = store.MarkCronFired(ctx, p.Request.Desired[0].ID, fired); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err = store.UpdateCron(ctx, command.ID, nil, nil, &disabled, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = store.(interface {
		SuspendCronsForApp(context.Context, string, string) (int, error)
	}).SuspendCronsForApp(ctx, app.ID, state.CronSuspendedNoLiveDeployment); err != nil {
		t.Fatal(err)
	}
	p.Request.Operation = "restore"
	restored, err := s.ApplyDeploymentCronStaging(ctx, app.ID, p)
	if err != nil || !restored.Changed {
		t.Fatalf("restore=%+v err=%v", restored, err)
	}
	after, err = s.DeploymentCronStagingSnapshot(ctx, p.AccountID, app.ID)
	if err != nil || !api.DeploymentCronsEqual(after.Crons, p.Request.Before) {
		t.Fatalf("restored snapshot=%+v err=%v", after, err)
	}
	current, err := store.CronByID(ctx, old.ID)
	if err != nil || !current.CreatedAt.Equal(old.CreatedAt) || !current.LastFiredAt.Equal(fired) {
		t.Fatalf("lost cron progress: %+v err=%v", current, err)
	}
	cmd, err := store.CronByID(ctx, command.ID)
	if err != nil || cmd.Enabled || len(cmd.Command) == 0 {
		t.Fatalf("command cron overwritten: %+v err=%v", cmd, err)
	}
	duplicate, err := s.ApplyDeploymentCronStaging(ctx, app.ID, p)
	if err != nil || duplicate.Changed || duplicate.Receipt != restored.Receipt {
		t.Fatalf("lost cleanup reply=%+v err=%v", duplicate, err)
	}
	p.Request.Operation = "stage"
	if _, err = s.ApplyDeploymentCronStaging(ctx, app.ID, p); !errors.Is(err, state.ErrDeploymentCronConflict) {
		t.Fatalf("delayed stage=%v", err)
	}
	deployments, err := store.ListDeploymentsForApp(ctx, app.ID, 10, 0)
	if err != nil || len(deployments) != 0 {
		t.Fatalf("cleanup created deployments=%v err=%v", deployments, err)
	}
}
func TestDeploymentCronJournalMemoryConflicts(t *testing.T) {
	testCronConflicts(t, func(t *testing.T) state.Store { return state.NewMemStore() })
}
func testCronConflicts(t *testing.T, factory func(*testing.T) state.Store) {
	for _, mode := range []string{"cleanup-first", "same-value", "aba", "new-deploy", "changed-intent", "wrong-app", "wrong-account", "same-desired-owner", "quota"} {
		t.Run(mode, func(t *testing.T) {
			store := factory(t)
			app, p, _ := cronStageFixture(t, store)
			s := store.(state.DeploymentCronStagingStore)
			ctx := t.Context()
			if mode == "cleanup-first" {
				p.Request.Operation = "restore"
				if _, err := s.ApplyDeploymentCronStaging(ctx, app.ID, p); err != nil {
					t.Fatal(err)
				}
				p.Request.Operation = "stage"
				if _, err := s.ApplyDeploymentCronStaging(ctx, app.ID, p); !errors.Is(err, state.ErrDeploymentCronConflict) {
					t.Fatalf("late=%v", err)
				}
				return
			}
			if mode == "quota" {
				p.Limits.CronLimitPerAccount = 2
				if _, err := s.ApplyDeploymentCronStaging(ctx, app.ID, p); !errors.Is(err, state.ErrCronQuotaExceeded) {
					t.Fatalf("quota=%v", err)
				}
				snapshot, _ := s.DeploymentCronStagingSnapshot(ctx, p.AccountID, app.ID)
				if !api.DeploymentCronsEqual(snapshot.Crons, p.Request.Before) {
					t.Fatal("quota failure partially applied")
				}
				return
			}
			if _, err := s.ApplyDeploymentCronStaging(ctx, app.ID, p); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "same-value", "aba":
				value := p.Request.Desired[0].Enabled
				if mode == "aba" {
					value = !value
				}
				if _, err := store.UpdateCron(ctx, p.Request.Desired[0].ID, nil, nil, &value, nil); err != nil {
					t.Fatal(err)
				}
				if mode == "aba" {
					value = !value
					if _, err := store.UpdateCron(ctx, p.Request.Desired[0].ID, nil, nil, &value, nil); err != nil {
						t.Fatal(err)
					}
				}
			case "new-deploy":
				if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64)}); err != nil {
					t.Fatal(err)
				}
			case "changed-intent":
				p.Request.Before[0].Timezone = "Asia/Tokyo"
			case "wrong-app":
				p.Request.ExpectedAppID = uuid.NewString()
			case "wrong-account":
				p.AccountID = uuid.NewString()
			case "same-desired-owner":
				snapshot, _ := s.DeploymentCronStagingSnapshot(ctx, p.AccountID, app.ID)
				newer := p
				newer.Request.SessionID = strings.Repeat("b", 32)
				newer.Request.ExpectedRevision = snapshot.Revision
				newer.Request.Before = append([]api.DeploymentCronConfig{}, snapshot.Crons...)
				if _, err := s.ApplyDeploymentCronStaging(ctx, app.ID, newer); err != nil {
					t.Fatal(err)
				}
				p.Request.Operation = "restore"
				if _, err := s.ApplyDeploymentCronStaging(ctx, app.ID, p); !errors.Is(err, state.ErrDeploymentCronConflict) {
					t.Fatalf("obsolete owner=%v", err)
				}
				newer.Request.Operation = "restore"
				if _, err := s.ApplyDeploymentCronStaging(ctx, app.ID, newer); err != nil {
					t.Fatal(err)
				}
				newer.Request.Operation = "stage"
				if _, err := s.ApplyDeploymentCronStaging(ctx, app.ID, newer); !errors.Is(err, state.ErrDeploymentCronConflict) {
					t.Fatalf("same-value delayed=%v", err)
				}
				return
			}
			p.Request.Operation = "restore"
			if _, err := s.ApplyDeploymentCronStaging(ctx, app.ID, p); err == nil {
				t.Fatal("unsafe cleanup succeeded")
			}
		})
	}
}
func TestDeploymentCronJournalMemoryConcurrency(t *testing.T) { testCronRace(t, state.NewMemStore()) }
func testCronRace(t *testing.T, store state.Store) {
	app, p, _ := cronStageFixture(t, store)
	s := store.(state.DeploymentCronStagingStore)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, op := range []string{"stage", "restore"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			request := p
			request.Request.Operation = op
			_, err := s.ApplyDeploymentCronStaging(t.Context(), app.ID, request)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil && !errors.Is(err, state.ErrDeploymentCronConflict) {
			t.Fatal(err)
		}
	}
	after, err := s.DeploymentCronStagingSnapshot(t.Context(), p.AccountID, app.ID)
	if err != nil || !api.DeploymentCronsEqual(after.Crons, p.Request.Before) {
		t.Fatalf("race=%+v err=%v", after, err)
	}
}

func TestDeploymentCronJournalMemoryOrdinaryEditRace(t *testing.T) {
	testCronOrdinaryEditRace(t, state.NewMemStore())
}
func testCronOrdinaryEditRace(t *testing.T, store state.Store) {
	app, p, _ := cronStageFixture(t, store)
	s := store.(state.DeploymentCronStagingStore)
	if _, err := s.ApplyDeploymentCronStaging(t.Context(), app.ID, p); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	result := make(chan error, 2)
	path := "/operator-wins"
	go func() {
		<-start
		_, err := store.UpdateCron(t.Context(), p.Request.Desired[0].ID, nil, &path, nil, nil)
		result <- err
	}()
	go func() {
		<-start
		p.Request.Operation = "restore"
		_, err := s.ApplyDeploymentCronStaging(t.Context(), app.ID, p)
		result <- err
	}()
	close(start)
	for range 2 {
		if err := <-result; err != nil && !errors.Is(err, state.ErrDeploymentCronConflict) {
			t.Fatal(err)
		}
	}
	cron, err := store.CronByID(t.Context(), p.Request.Desired[0].ID)
	if err != nil || cron.Path != path {
		t.Fatalf("operator edit lost: %+v err=%v", cron, err)
	}
}
func TestDeploymentCronJournalMemoryAccountQuotaRace(t *testing.T) {
	testCronAccountQuotaRace(t, state.NewMemStore())
}
func testCronAccountQuotaRace(t *testing.T, store state.Store) {
	app, p, _ := cronStageFixture(t, store)
	other, err := store.CreateApp(t.Context(), state.App{AccountID: app.AccountID, Slug: "other-cron-app"})
	if err != nil {
		t.Fatal(err)
	}
	p.Limits.CronLimitPerApp = 5
	p.Limits.CronLimitPerAccount = 4
	p.Request.Desired = append(p.Request.Desired, api.DeploymentCronConfig{ID: uuid.NewString(), Schedule: "0 1 * * *", Path: "/extra", Enabled: true, Timezone: "UTC"})
	start := make(chan struct{})
	result := make(chan error, 2)
	go func() {
		<-start
		_, err := store.(state.DeploymentCronStagingStore).ApplyDeploymentCronStaging(t.Context(), app.ID, p)
		result <- err
	}()
	go func() {
		<-start
		_, err := store.CreateCronIfUnderQuota(t.Context(), other.ID, "0 2 * * *", "/tick", true, p.Limits)
		result <- err
	}()
	close(start)
	success := 0
	for range 2 {
		err := <-result
		if err == nil {
			success++
		} else if !errors.Is(err, state.ErrCronQuotaExceeded) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("quota admissions=%d", success)
	}
}
func TestDeploymentCronJournalMemoryCommandIdentityCollision(t *testing.T) {
	testCronIdentityCollision(t, state.NewMemStore())
}
func testCronIdentityCollision(t *testing.T, store state.Store) {
	app, p, command := cronStageFixture(t, store)
	p.Request.Desired[1].ID = command.ID
	s := store.(state.DeploymentCronStagingStore)
	if _, err := s.ApplyDeploymentCronStaging(t.Context(), app.ID, p); !errors.Is(err, state.ErrDeploymentCronConflict) {
		t.Fatalf("command identity accepted: %v", err)
	}
	snapshot, err := s.DeploymentCronStagingSnapshot(t.Context(), p.AccountID, app.ID)
	if err != nil || !api.DeploymentCronsEqual(snapshot.Crons, p.Request.Before) || snapshot.Revision != p.Request.ExpectedRevision {
		t.Fatalf("collision partially mutated: %+v err=%v", snapshot, err)
	}
}
