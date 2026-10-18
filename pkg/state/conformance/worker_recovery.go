// adr: 20261017221000
package conformance

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func recoveryWorker(t *testing.T, fx *Fixture) {
	t.Helper()
	manifest := fx.App.Manifest
	manifest.ExecutionMode = api.ExecutionModeWorker
	if _, err := fx.Store.UpdateApp(fx.Ctx, fx.App.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
}

// ADR-20261017221000: claims, cooldown and stale-worker fencing must hold on both stores.
func testWorkerRecoveryClaims(t *testing.T, fx *Fixture) {
	recoveryWorker(t, fx)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	if _, err := fx.Store.WorkerRecoveryByApp(fx.Ctx, fx.App.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing ledger: %v", err)
	}
	for _, token := range []string{"", "invalid", uuid.Nil.String(), "00000000000000000000000000000000"} {
		if _, _, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "rev1", token, now, now.Add(time.Minute)); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid claim token %q accepted: %v", token, err)
		}
	}
	const contenders = 8
	var wg sync.WaitGroup
	winners := make(chan state.WorkerRecovery, contenders)
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "rev1", uuid.NewString(), now, now.Add(time.Minute))
			if err != nil {
				t.Error(err)
			}
			if claimed {
				winners <- r
			}
		}()
	}
	wg.Wait()
	close(winners)
	if len(winners) != 1 {
		t.Fatalf("claim winners: %d, want 1", len(winners))
	}
	r := <-winners
	oldToken := r.ClaimToken
	if _, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "rev2", uuid.NewString(), now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("revision must not steal an active lease: claimed=%v err=%v", claimed, err)
	}
	r.Status, r.Failures, r.UpdatedAt, r.NextAttemptAt = "retrying_startup", 3, now, now.Add(20*time.Second)
	if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, oldToken, r); err != nil {
		t.Fatal(err)
	}
	stored, err := fx.Store.WorkerRecoveryByApp(fx.Ctx, fx.App.ID)
	if err != nil || stored.Status != "retrying_startup" || stored.Failures != 3 || !stored.NextAttemptAt.Equal(now.Add(20*time.Second)) || stored.ClaimToken != "" {
		t.Fatalf("persisted retry: %+v err=%v", stored, err)
	}
	if ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", now.Add(19*time.Second), 10); err != nil || len(ids) != 0 {
		t.Fatalf("retry not due: %v err=%v", ids, err)
	}
	held, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "rev1", uuid.NewString(), now.Add(time.Second), now.Add(time.Minute))
	if err != nil || !claimed || held.Failures != stored.Failures || !held.NextAttemptAt.Equal(stored.NextAttemptAt) || held.Status != stored.Status {
		t.Fatalf("event lost cooldown while claiming scale-in lease: %+v claimed=%v err=%v", held, claimed, err)
	}
	if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, held.ClaimToken, held); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", now.Add(20*time.Second), 10); err != nil || !slices.Equal(ids, []string{fx.App.ID}) {
		t.Fatalf("retry due: %v err=%v", ids, err)
	}
	newToken := uuid.NewString()
	r, claimed, err = fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "rev2", newToken, now.Add(time.Second), now.Add(time.Minute))
	if err != nil || !claimed || r.Failures != 0 {
		t.Fatalf("new intent resets cooldown: %+v claimed=%v err=%v", r, claimed, err)
	}
	r.Status, r.NextAttemptAt = "ready", now.Add(30*time.Second)
	if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, oldToken, r); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale completion: %v", err)
	}
	expired := r
	expired.UpdatedAt, expired.NextAttemptAt = now.Add(time.Minute), now.Add(90*time.Second)
	if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, newToken, expired); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired lease completed before reclamation: %v", err)
	}
	// A process can die without completing. Another scheduler reclaims the
	// expired lease; its predecessor cannot overwrite the new retry state.
	r, claimed, err = fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "rev2", uuid.NewString(), now.Add(time.Minute), now.Add(2*time.Minute))
	if err != nil || !claimed {
		t.Fatalf("expired lease not reclaimed: claimed=%v err=%v", claimed, err)
	}
	r.Status, r.Failures, r.NextAttemptAt = "ready", 0, now.Add(90*time.Second)
	if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, newToken, r); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker completion: %v", err)
	}
	if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, r.ClaimToken, r); err != nil {
		t.Fatal(err)
	}
	r.Status = "arbitrary_error_text"
	if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, r.ClaimToken, r); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unbounded status accepted: %v", err)
	}
}

// ADR-20261017221000: discovery must neither revive stopped workloads nor cross owners.
func testWorkerRecoveryCandidates(t *testing.T, fx *Fixture) {
	now := time.Now().UTC()
	if ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", now, 10); err != nil || len(ids) != 0 {
		t.Fatalf("request-mode app selected: %v %v", ids, err)
	}
	recoveryWorker(t, fx)
	if err := fx.Store.SetAppNodeID(fx.Ctx, fx.App.ID, fx.Node.ID); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, uuid.NewString(), now, 10); err != nil || len(ids) != 0 {
		t.Fatalf("foreign app selected: %v %v", ids, err)
	}
	if _, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, uuid.NewString(), "foreign", uuid.NewString(), now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("foreign app claimed: %v %v", claimed, err)
	}
	if ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, fx.Node.ID, now, 1); err != nil || !slices.Equal(ids, []string{fx.App.ID}) {
		t.Fatalf("owned app omitted: %v %v", ids, err)
	}
	if ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", now, 0); err != nil || len(ids) != 0 {
		t.Fatalf("zero batch limit: %v %v", ids, err)
	}
	inactive := state.AppEvictedCold
	if _, err := fx.Store.UpdateApp(fx.Ctx, fx.App.ID, state.UpdateAppParams{Status: &inactive}); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", now, 10); err != nil || len(ids) != 0 {
		t.Fatalf("inactive app selected: %v %v", ids, err)
	}
	if _, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "r", uuid.NewString(), now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("inactive app claimed: %v %v", claimed, err)
	}
	active := state.AppActive
	if _, err := fx.Store.UpdateApp(fx.Ctx, fx.App.ID, state.UpdateAppParams{Status: &active}); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateAccountStatus(fx.Ctx, fx.Account.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", now, 10); err != nil || len(ids) != 0 {
		t.Fatalf("suspended account selected: %v %v", ids, err)
	}
	if _, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "r", uuid.NewString(), now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("suspended account claimed: %v %v", claimed, err)
	}
	if err := fx.Store.UpdateAccountStatus(fx.Ctx, fx.Account.ID, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", now, 10); err != nil || len(ids) != 1 {
		t.Fatalf("past-due grace should preserve workers: %v %v", ids, err)
	}
	holds, ok := fx.Store.(state.AccountAbuseHoldStore)
	if !ok {
		t.Fatal("store does not implement account abuse holds")
	}
	if _, err := holds.SetAccountAbuseHold(fx.Ctx, fx.Account.ID, state.AccountAbuseHoldOperator, now); err != nil {
		t.Fatal(err)
	}
	if ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", now, 10); err != nil || len(ids) != 0 {
		t.Fatalf("abuse-held account selected: %v %v", ids, err)
	}
	if _, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "r", uuid.NewString(), now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("abuse-held account claimed: %v %v", claimed, err)
	}
}

func testWorkerRecoveryCapturedEnvironment(t *testing.T, fx *Fixture) {
	specs, ok := fx.Store.(state.ProjectEnvironmentWorkloadSpecStore)
	if !ok {
		t.Fatal("store does not support captured workload settings")
	}
	project, err := fx.Store.CreateProject(fx.Ctx, state.Project{AccountID: fx.Account.ID, Slug: "recovery-captured"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, ProjectID: project.ID, Slug: "captured-worker", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 128, MaxConcurrency: 3, WorkloadClass: state.WorkloadClassHTTP,
		Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeRequest}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.CreateProjectEnvironment(fx.Ctx, state.ProjectEnvironment{AccountID: fx.Account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.Manifest.ExecutionMode, settings.WorkloadClass = api.ExecutionModeWorker, state.WorkloadClassWorker
	if _, err := specs.PutProjectEnvironmentWorkloadSpec(fx.Ctx, fx.Account.ID, project.ID, "staging", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "sha256:worker", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.MarkDeploymentLive(fx.Ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", time.Now().UTC(), 10)
	if err != nil || !slices.Equal(ids, []string{app.ID}) {
		t.Fatalf("captured worker missing behind request head: %v %v", ids, err)
	}
}

func testWorkerRecoveryModeCleanup(t *testing.T, fx *Fixture) {
	recoveryWorker(t, fx)
	ins, err := fx.Store.CreateInstanceWithMode(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateRunning), fx.App.RAMMB, fx.Node.ID, uuid.NewString(), string(state.InstanceModeWorker))
	if err != nil {
		t.Fatal(err)
	}
	manifest := fx.App.Manifest
	manifest.ExecutionMode = api.ExecutionModeRequest
	if _, err := fx.Store.UpdateApp(fx.Ctx, fx.App.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	ids, err := fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", time.Now().UTC(), 10)
	if err != nil || !slices.Equal(ids, []string{fx.App.ID}) {
		t.Fatalf("resident worker cleanup was lost after mode switch: %v %v", ids, err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	ids, err = fx.Store.ListWorkerRecoveryApps(fx.Ctx, "", time.Now().UTC(), 10)
	if err != nil || len(ids) != 0 {
		t.Fatalf("finished cleanup remained due: %v %v", ids, err)
	}
}
