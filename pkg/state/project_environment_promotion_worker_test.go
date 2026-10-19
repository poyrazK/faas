package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionWorkerMemory(t *testing.T) {
	testPromotionWorker(t, func(*testing.T) Store { return NewMemStore() })
}

func newPromotionWorkerFixture(t *testing.T, store Store) *promotionSnapshotFixture {
	t.Helper()
	f := newPromotionSnapshotFixture(t, store, false, false, false, true)
	f.promotion.HealthPolicy = &api.ProjectPromotionHealthPolicy{Worker: copyPromotionWorker(f.view.Worker), Probes: []api.ProjectReleaseProbe{{AppID: f.apps[0].ID, Slug: f.apps[0].Slug, Method: "GET", Path: "/health"}}}
	f.create(t)
	f.prepare(t)
	return f
}
func seedPromotionWorkerRuntime(t *testing.T, f *promotionSnapshotFixture, id string) Instance {
	t.Helper()
	ctx := t.Context()
	node, err := f.store.ComputeNodeByName(ctx, DefaultLocalNodeName)
	requirePromotionSnapshot(t, err)
	ins, err := f.store.CreateInstanceWithMode(ctx, f.apps[1].ID, id, string(StateColdBooting), 256, node.ID, uuid.NewString(), string(InstanceModeWorker))
	requirePromotionSnapshot(t, err)
	snap, err := f.store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.apps[1].ID, id)
	requirePromotionSnapshot(t, err)
	fence, err := NewRuntimeAppConfigFence(snap)
	requirePromotionSnapshot(t, err)
	dep, err := f.store.DeploymentByID(ctx, id)
	requirePromotionSnapshot(t, err)
	inputs := RuntimeConfigInputs{Scope: dep.Scope, Boundary: time.Now(), Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}
	for _, v := range snap.Values {
		inputs.Variables[v.Key] = v.Value
	}
	for _, v := range snap.Secrets {
		inputs.SecretVersions[v.Scope+"/"+v.Key] = max(1, v.SecretVersion)
	}
	ins, err = f.store.(RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(ctx, RuntimeInstancePublication{AccountID: f.account.ID, AppID: f.apps[1].ID, InstanceID: ins.ID, NodeID: node.ID, WakeID: ins.WakeID, ExpectedState: ins.State, Netns: "promotion-" + ins.ID, HostIP: "10.100.0.8", GuestUID: 20008, Fence: fence.SecretFence, ConfigFence: fence, Inputs: &inputs})
	requirePromotionSnapshot(t, err)
	return ins
}
func promotionWorkerRound(t *testing.T, f *promotionSnapshotFixture, failures ...int) *api.ProjectPromotionHealth {
	t.Helper()
	hs := f.store.(ProjectPromotionHealthStore)
	h, err := hs.ProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID)
	requirePromotionSnapshot(t, err)
	if h.CheckedAt != nil {
		at := time.Now().Add(-api.ProjectReleaseVerificationInterval - time.Second)
		h.CheckedAt = &at
		setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
	}
	candidateErrors := 0
	if len(failures) > 0 {
		candidateErrors = failures[0]
	}
	h, err = hs.AdvanceProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: h.Generation, Evidence: promotionHealthEvidence(h.Policy, candidateErrors, 0)})
	requirePromotionSnapshot(t, err)
	return h
}
func claimPromotionWorkerJob(t *testing.T, f *promotionSnapshotFixture, id string) Invocation {
	t.Helper()
	var inv Invocation
	var err error
	if s, ok := f.store.(*PgStore); ok {
		var trigger string
		requirePromotionSnapshot(t, s.pool.QueryRow(t.Context(), "SELECT id::text FROM triggers WHERE app_id=$1 LIMIT 1", f.apps[1].ID).Scan(&trigger))
		inv, err = s.ClaimQueueTriggerInvocation(t.Context(), id, trigger, f.apps[1].ID, "orders", 60)
	} else {
		inv, err = f.store.ClaimInvocation(t.Context(), id, "", 60)
	}
	requirePromotionSnapshot(t, err)
	inv.Source = InvocationSource("esm")
	return inv
}
func enqueuePromotionWorkerJob(t *testing.T, f *promotionSnapshotFixture) Invocation {
	t.Helper()
	inv, err := f.store.EnqueueInvocation(t.Context(), Invocation{AppID: f.apps[1].ID, AccountID: f.account.ID, QueueBindingID: f.view.Worker.BindingID, QueueName: "orders", DeploymentScope: "production", Source: InvocationQueue, Payload: json.RawMessage(`{}`), Headers: json.RawMessage(`{}`), DueAt: time.Now()})
	requirePromotionSnapshot(t, err)
	return claimPromotionWorkerJob(t, f, inv.ID)
}
func testPromotionWorker(t *testing.T, factory func(*testing.T) Store) {
	testPromotionWorkerMode(t, factory, false)
}

func testPromotionWorkerMode(t *testing.T, factory func(*testing.T) Store, coordinated bool) {
	for _, scenario := range []string{"success", "policy_copy", "old_controller", "backlog_cutover", "source_runtime", "startup_missing", "baseline_missing", "low_traffic", "reused_window", "binding_drift", "worker_loss_cutover", "job_regression", "http_regression_low_traffic", "baseline_regression", "restart_hold"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			var f *promotionSnapshotFixture
			if coordinated {
				f = newCoordinatedPromotionFixture(t, factory(t))
				f.create(t)
				f.prepare(t)
				completeCoordinatedMigration(t, f)
			} else {
				f = newPromotionWorkerFixture(t, factory(t))
			}
			worker := f.apps[1]
			if scenario == "source_runtime" {
				seedPromotionWorkerRuntime(t, f, f.sources[1].ID)
				var ready string
				var err error
				switch s := f.store.(type) {
				case *MemStore:
					s.mu.Lock()
					ready, err = s.releaseWorkerReadyLocked(f.account.ID, worker.ID, f.sources[1].ID)
					s.mu.Unlock()
				case *PgStore:
					ready, err = releaseWorkerReadyDB(ctx, s.pool, f.account.ID, worker.ID, f.sources[1].ID)
				}
				requirePromotionSnapshot(t, err)
				if ready == "" {
					t.Fatal("source runtime proof did not preserve staging scope")
				}
				return
			}
			hs := f.store.(ProjectPromotionHealthStore)
			if scenario == "policy_copy" {
				h, err := hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
				h.Policy.Worker.BindingHash = "changed"
				again, err := hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
				if again.Policy.Worker.BindingHash != f.view.Worker.BindingHash {
					t.Fatal("public policy mutated durable worker identity")
				}
				return
			}
			if scenario == "old_controller" {
				if s, ok := f.store.(*PgStore); ok {
					if _, err := s.pool.Exec(ctx, "UPDATE project_environment_promotion_health SET payload=payload #- '{policy,worker}' WHERE promotion_id=$1", f.promotion.ID); err == nil {
						t.Fatal("older controller erased worker contract")
					}
				}
				return
			}

			held, err := f.store.(ProjectReleaseWorkerStore).HeldProjectReleaseWorkers(ctx, worker.ID)
			requirePromotionSnapshot(t, err)
			if len(held) != 1 || held[0] != f.candidates[1].DeploymentID {
				t.Fatal("candidate hold missing", held)
			}
			if scenario == "restart_hold" {
				if s, ok := f.store.(*PgStore); ok {
					f.store = NewPgStore(s.pool.Pool)
				}
				held, err = f.store.(ProjectReleaseWorkerStore).HeldProjectReleaseWorkers(ctx, worker.ID)
				requirePromotionSnapshot(t, err)
				if len(held) != 1 {
					t.Fatal("restart lost candidate")
				}
				return
			}
			before := enqueuePromotionWorkerJob(t, f)
			_, version, err := ResolveInvocationVersion(ctx, f.store, before)
			requirePromotionSnapshot(t, err)
			if version.DeploymentID != f.previous[1].ID {
				t.Fatal("production job reached dark worker")
			}
			if scenario != "baseline_missing" {
				SeedProjectWorkerJobSamplesForTest(t, f.store, worker.ID, f.account.ID, f.view.Worker.BindingID, f.previous[1].ID, time.Now().Add(-time.Second), false)
			}
			var candidate Instance
			if scenario != "startup_missing" {
				candidate = seedPromotionWorkerRuntime(t, f, f.candidates[1].DeploymentID)
			}
			if scenario == "startup_missing" || scenario == "baseline_missing" {
				h := promotionWorkerRound(t, f)
				if h.Phase != "preflight" || h.Verdict != "unknown" {
					t.Fatal("unqualified worker allowed cutover", h)
				}
				return
			}
			claimed, err := f.store.(ProjectReleaseWorkerStore).ClaimProjectWorkerDrain(ctx, worker.ID, candidate.ID, f.previous[1].ID)
			requirePromotionSnapshot(t, err)
			if claimed {
				t.Fatal("candidate drained before cutover")
			}
			if scenario == "binding_drift" {
				limit := 3
				_, err = f.store.(QueueBindingConsumerStore).UpdateQueueBindingWithConsumer(ctx, f.account.ID, worker.ID, f.view.Worker.BindingID, UpdateQueueBindingParams{MaxConcurrency: &limit})
				requirePromotionSnapshot(t, err)
				h := promotionWorkerRound(t, f)
				if h.Phase != "conflicted" {
					t.Fatal("changed binding admitted", h)
				}
				return
			}
			promotionWorkerRound(t, f)
			h := promotionWorkerRound(t, f)
			if h.Phase != "cutover" {
				t.Fatal("ready worker did not allow cutover", h)
			}
			if scenario == "worker_loss_cutover" {
				requirePromotionSnapshot(t, f.store.UpdateInstanceState(ctx, candidate.ID, string(StateStopped)))
				_, err = f.store.(ProjectEnvironmentPromotionReleaseSetStore).PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
				if !errors.Is(err, ErrPromotionWorkerNotReady) {
					t.Fatal("lost worker published", err)
				}
				return
			}
			if scenario == "backlog_cutover" {
				for range 3 {
					_, err := f.store.EnqueueInvocation(ctx, Invocation{AppID: worker.ID, AccountID: f.account.ID, QueueBindingID: f.view.Worker.BindingID, QueueName: "orders", DeploymentScope: "production", Source: InvocationQueue, Payload: json.RawMessage(`{}`), DueAt: time.Now()})
					requirePromotionSnapshot(t, err)
				}
			}
			graph, err := f.store.(ProjectEnvironmentPromotionReleaseSetStore).PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
			requirePromotionSnapshot(t, err)
			if scenario == "backlog_cutover" {
				h, err = hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
				if h.WorkerHealth.BaselinePendingJobs != 8 {
					t.Fatal("backlog not snapshotted at publication", h.WorkerHealth)
				}
				return
			}
			// A job admitted before publication keeps its graph even when resolved again.
			_, version, err = ResolveInvocationVersion(ctx, f.store, before)
			requirePromotionSnapshot(t, err)
			if version.DeploymentID != f.previous[1].ID {
				t.Fatal("job retry changed release")
			}
			next := enqueuePromotionWorkerJob(t, f)
			_, version, err = ResolveInvocationVersion(ctx, f.store, next)
			requirePromotionSnapshot(t, err)
			if version.DeploymentID != f.candidates[1].DeploymentID || version.ReleaseID != graph.ID {
				t.Fatal("new job did not select complete new graph")
			}
			h, err = hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			cutover := time.Now().Add(-3 * time.Minute)
			h.StartedAt = cutover
			h.Deadline = cutover.Add(api.ProjectReleaseVerificationTimeout)
			h.CheckedAt = nil
			setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
			SeedProjectWorkerJobSamplesForTest(t, f.store, worker.ID, f.account.ID, f.view.Worker.BindingID, f.previous[1].ID, cutover.Add(-time.Second), scenario == "baseline_regression")
			if scenario != "low_traffic" && scenario != "http_regression_low_traffic" {
				SeedProjectWorkerJobSamplesForTest(t, f.store, worker.ID, f.account.ID, f.view.Worker.BindingID, f.candidates[1].DeploymentID, cutover.Add(time.Second), scenario == "job_regression")
			}
			if scenario == "http_regression_low_traffic" {
				h = promotionWorkerRound(t, f, 1)
			} else {
				h = promotionWorkerRound(t, f)
			}
			if scenario == "low_traffic" || scenario == "baseline_regression" {
				if h.Phase != "verifying" || h.Verdict != "unknown" {
					t.Fatal("unknown worker health reported success", h)
				}
				return
			}
			if scenario == "reused_window" {
				h = promotionWorkerRound(t, f)
				if h.Phase != "verifying" || h.HealthyRounds != 0 {
					t.Fatal("same worker deliveries counted twice", h)
				}
				return
			}
			// Keep the second delivery window separate from the first sample's records.
			if scenario == "http_regression_low_traffic" {
				h = promotionWorkerRound(t, f, 1)
			} else {
				SeedProjectWorkerJobSamplesForTest(t, f.store, worker.ID, f.account.ID, f.view.Worker.BindingID, f.candidates[1].DeploymentID, time.Now().Add(-time.Second), scenario == "job_regression")
				h = promotionWorkerRound(t, f)
			}
			if scenario == "job_regression" || scenario == "http_regression_low_traffic" {
				if h.Phase != "rolling_back" {
					t.Fatal("worker regression did not initiate restoration", h)
				}
				_, err = f.store.(ProjectEnvironmentPromotionReleaseSetStore).RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, coordinatedBaselineMembers(f))
				if !errors.Is(err, ErrPromotionWorkerNotReady) {
					t.Fatal("rollback bypassed baseline worker readiness", err)
				}
				held, err = f.store.(ProjectReleaseWorkerStore).HeldProjectReleaseWorkers(ctx, worker.ID)
				requirePromotionSnapshot(t, err)
				if len(held) != 1 || held[0] != f.previous[1].ID {
					t.Fatal("baseline recovery hold missing", held)
				}
				seedPromotionWorkerRuntime(t, f, f.previous[1].ID)
				_, err = f.store.(ProjectEnvironmentPromotionReleaseSetStore).RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, coordinatedBaselineMembers(f))
				requirePromotionSnapshot(t, err)
				return
			}
			if h.Phase != "finalizing" {
				t.Fatal("worker deliveries did not allow finalization", h)
			}
			h, err = hs.AdvanceProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: h.Generation, Action: "complete"})
			requirePromotionSnapshot(t, err)
			if h.Phase != "succeeded" {
				t.Fatal("verified mixed release did not finish", h)
			}
		})
	}
}

func TestPromotionWorkerServiceGraph(t *testing.T) {
	f := newPromotionSnapshotFixture(t, NewMemStore(), false, true, false, true)
	f.promotion.HealthPolicy = &api.ProjectPromotionHealthPolicy{Worker: copyPromotionWorker(f.view.Worker), Probes: []api.ProjectReleaseProbe{{AppID: f.apps[0].ID, Slug: f.apps[0].Slug, Method: "GET", Path: "/health"}}}
	f.create(t)
	f.prepare(t)
	route, managed, found, err := f.store.(ProjectServiceGraphStore).ResolveProjectServiceBinding(t.Context(), f.apps[1].ID, f.candidates[1].DeploymentID, "api")
	requirePromotionSnapshot(t, err)
	if !managed || !found || route.TargetDeploymentID != f.candidates[0].DeploymentID || route.ReleaseSetID != f.promotion.ID {
		t.Fatal("worker did not resolve its candidate HTTP peer", route, managed, found)
	}
}

func TestPromotionWorkerRetainedSettings(t *testing.T) {
	worker := &api.ProjectReleaseWorker{DrainSeconds: 30}
	for _, tc := range []struct {
		name  string
		class WorkloadClass
		mode  string
		grace int
		valid bool
	}{{"worker", WorkloadClassWorker, api.ExecutionModeWorker, 30, true}, {"retained_http", WorkloadClassHTTP, api.ExecutionModeRequest, 30, false}, {"changed_grace", WorkloadClassWorker, api.ExecutionModeWorker, 60, false}} {
		t.Run(tc.name, func(t *testing.T) {
			settings := ProjectEnvironmentWorkloadSettings{WorkloadClass: tc.class, Manifest: AppManifest{ExecutionMode: tc.mode, StopGracePeriodS: tc.grace}}
			if (validatePromotionWorkerBaseline(settings, worker) == nil) != tc.valid {
				t.Fatal("retained worker settings misclassified")
			}
		})
	}
}
