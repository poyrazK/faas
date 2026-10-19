// adr:1659 — resumable migration evidence over existing runtime owners.
package migrationverify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func fixture(t *testing.T) (Backend, *state.MemStore, *Snapshot, api.CreateMigrationVerificationRequest) {
	t.Helper()
	ctx := t.Context()
	m := state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "migrate@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "migrate", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:migrate"})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetDeploymentRootfs(ctx, dep.ID, "/rootfs", "rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	dep, err = m.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{App: app, Deployment: dep, ChangedAt: time.Now().UTC(), RuntimeCurrent: true}
	b := Backend{Store: m, Tasks: m, AccountID: acct.ID, AppID: app.ID, App: app.Slug, Snapshot: func(context.Context) (Snapshot, error) { return *snap, nil },
		Smoke: func(context.Context, api.MigrationVerification) (api.MigrationVerificationCheck, error) {
			return api.MigrationVerificationCheck{Status: "passed", Detail: "proven"}, nil
		},
		Park: func(context.Context, api.MigrationVerification) error { return nil }, Drained: func(context.Context) (bool, error) { return true, nil }, Wake: func(context.Context, api.MigrationVerification) error { return nil }, Running: func(context.Context, api.MigrationVerification) (string, bool, error) { return "instance", true, nil }, Restore: func(context.Context, api.MigrationVerification) (string, error) { return "restored", nil }}
	in := api.CreateMigrationVerificationRequest{ID: uuid.NewString(), DeploymentID: dep.ID, Checks: api.MigrationDependencyChecks{PostgresEnvKey: "DATABASE_URL"}, SmokePath: "/ready", ColdWake: true}
	return b, m, snap, in
}

func completeDependency(t *testing.T, m *state.MemStore, check string, status state.AppTaskStatus, output string) {
	t.Helper()
	ctx := t.Context()
	claimed, err := m.ClaimNextAppTask(ctx, "fixture", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.MarkAppTaskRunning(ctx, claimed.ID, *claimed.LeaseToken, time.Now()); err != nil {
		t.Fatal(err)
	}
	if output == "" {
		raw, _ := json.Marshal(api.MigrationDependencyReport{Checks: []api.MigrationVerificationCheck{{Name: check, Status: "passed", Detail: "PRIVATE_URL_OR_SECRET"}}})
		output = string(raw)
	}
	exit := 0
	if status != state.AppTaskSucceeded {
		exit = 1
	}
	params := state.CompleteAppTaskParams{ID: claimed.ID, LeaseToken: *claimed.LeaseToken, Status: status, ExitCode: &exit, StdoutTail: output, FinishedAt: time.Now()}
	if status != state.AppTaskSucceeded {
		code, message := "dependency_failed", "fixture failure"
		params.FailureCode = &code
		params.FailureMessage = &message
	}
	_, err = m.CompleteAppTask(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
}
func advance(t *testing.T, b Backend, id string) api.MigrationVerification {
	t.Helper()
	r, err := b.Advance(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMigrationVerificationResumesThroughColdWake(t *testing.T) {
	b, m, _, in := fixture(t)
	run, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	run = advance(t, b, run.ID)
	first := run.DependencyTaskID
	if first == "" {
		t.Fatal("no dependency task")
	}
	// Lose task admission and creation responses: both identities must survive.
	again, err := b.Create(t.Context(), in)
	if err != nil || again.ID != run.ID || again.DependencyTaskID != first {
		t.Fatalf("recreate: %+v %v", again, err)
	}
	run = advance(t, b, run.ID)
	if run.DependencyTaskID != first {
		t.Fatal("duplicate dependency task")
	}
	completeDependency(t, m, "postgres", state.AppTaskSucceeded, "")
	for range 10 {
		run = advance(t, b, run.ID)
		if run.ColdDependencyTaskID != "" {
			break
		}
	}
	if run.ColdDependencyTaskID == "" || run.WakeID == "" || run.DrainedAt == nil || run.InstanceID == "" {
		t.Fatalf("missing wake evidence: %+v", run)
	}
	if run.Status != "pending" {
		t.Fatal("passed before post-wake dependencies")
	}
	completeDependency(t, m, "postgres", state.AppTaskSucceeded, "")
	run = advance(t, b, run.ID)
	if run.Status != "passed" || len(run.Checks) != 5 {
		t.Fatalf("report: %+v", run)
	}
	raw, _ := json.Marshal(run)
	if strings.Contains(string(raw), "PRIVATE_URL_OR_SECRET") {
		t.Fatal("reflected raw guest output")
	}
	rows, err := m.ListAppTasks(t.Context(), b.AccountID, b.AppID, 20, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("tasks=%d err=%v", len(rows), err)
	}
}
func TestMigrationVerificationDoesNotPassIncompleteOrFailedDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		status       state.AppTaskStatus
	}{{"missing", `{"checks":[]}`, state.AppTaskSucceeded}, {"wrong", `{"checks":[{"name":"object_storage","status":"passed"}]}`, state.AppTaskSucceeded}, {"failed task", "", state.AppTaskFailed}, {"invalid output", "not json", state.AppTaskSucceeded}} {
		t.Run(tc.name, func(t *testing.T) {
			b, m, _, in := fixture(t)
			r, err := b.Create(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			r = advance(t, b, r.ID)
			completeDependency(t, m, "postgres", tc.status, tc.output)
			r = advance(t, b, r.ID)
			if r.Status != "failed" {
				t.Fatalf("accepted incomplete task: %+v", r)
			}
		})
	}
}
func TestMigrationVerificationDriftAndAccountIsolation(t *testing.T) {
	b, m, s, in := fixture(t)
	r, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	foreign := b
	foreign.AccountID = uuid.NewString()
	if _, err = foreign.Get(t.Context(), r.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign read: %v", err)
	}
	if _, err = foreign.Cancel(t.Context(), r.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign cancellation: %v", err)
	}
	s.ChangedAt = s.ChangedAt.Add(time.Second)
	r = advance(t, b, r.ID)
	if r.Status != "blocked" || r.DependencyTaskID != "" {
		t.Fatalf("drift admitted work: %+v", r)
	}
	rows, err := m.ListAppTasks(t.Context(), b.AccountID, b.AppID, 20, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("tasks after drift: %d %v", len(rows), err)
	}
}
func TestMigrationVerificationWaitsForDrainAndCorrelatedWake(t *testing.T) {
	b, m, _, in := fixture(t)
	drained := false
	ready := false
	wakeCalls := 0
	b.Drained = func(context.Context) (bool, error) { return drained, nil }
	b.Wake = func(context.Context, api.MigrationVerification) error { wakeCalls++; return nil }
	b.Running = func(context.Context, api.MigrationVerification) (string, bool, error) { return "instance", ready, nil }
	r, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	r = advance(t, b, r.ID)
	completeDependency(t, m, "postgres", state.AppTaskSucceeded, "")
	for range 4 {
		r = advance(t, b, r.ID)
	}
	if r.Phase != "drain" || wakeCalls != 0 {
		t.Fatalf("woke before drain: %+v", r)
	}
	drained = true
	r = advance(t, b, r.ID)
	r = advance(t, b, r.ID)
	r = advance(t, b, r.ID)
	if r.Phase != "waking" || r.Status != "pending" {
		t.Fatalf("accepted uncorrelated wake: %+v", r)
	}
	if wakeCalls < 2 {
		t.Fatal("lost wake publication was not retried")
	}
	ready = true
	r = advance(t, b, r.ID)
	if r.Phase != "cold_smoke" {
		t.Fatalf("wake: %+v", r)
	}
}
func TestMigrationVerificationConcurrentAdvancesAdmitOneTask(t *testing.T) {
	b, m, _, in := fixture(t)
	r, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { _, _ = b.Advance(t.Context(), r.ID) })
	}
	wg.Wait()
	rows, err := m.ListAppTasks(t.Context(), b.AccountID, b.AppID, 20, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("concurrent tasks=%d %v", len(rows), err)
	}
}

func TestMigrationVerificationCanonicalDeploymentSelector(t *testing.T) {
	b, _, _, in := fixture(t)
	id, err := uuid.Parse(in.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	in.DeploymentID = strings.ToUpper(id.String())
	r, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Request.DeploymentID != id.String() {
		t.Fatal("deployment selector was not normalized")
	}
	if replay, err := b.Create(t.Context(), in); err != nil || replay.ID != r.ID {
		t.Fatalf("selector replay: %+v %v", replay, err)
	}
}
func TestMigrationVerificationSmokeInfrastructureRecovery(t *testing.T) {
	b, m, _, in := fixture(t)
	in.ColdWake = false
	r, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	r = advance(t, b, r.ID)
	completeDependency(t, m, "postgres", state.AppTaskSucceeded, "")
	r = advance(t, b, r.ID)
	original := b.Smoke
	b.Smoke = func(context.Context, api.MigrationVerification) (api.MigrationVerificationCheck, error) {
		return api.MigrationVerificationCheck{}, errors.New("gateway unavailable")
	}
	if _, err = b.Advance(t.Context(), r.ID); err == nil {
		t.Fatal("infrastructure error lost")
	}
	b.Smoke = original
	r = advance(t, b, r.ID)
	if r.Status != "passed" {
		t.Fatalf("retry: %+v", r)
	}
}
