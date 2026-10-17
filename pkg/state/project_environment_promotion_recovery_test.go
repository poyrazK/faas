package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionRecoveryMemory(t *testing.T) {
	testPromotionRecovery(t, func(*testing.T) Store { return NewMemStore() })
}

func promotionRecoveryFailureFixture(t *testing.T, store Store, known ...bool) (*promotionSnapshotFixture, api.ProjectPromotionMigrationRecoveryRequest) {
	t.Helper()
	ctx := t.Context()
	var f *promotionSnapshotFixture
	if len(known) > 2 && known[2] {
		f = newPromotionWorkerSetRawFixture(t, store, true)
	} else if len(known) > 1 && known[1] {
		f = newCoordinatedPromotionFixture(t, store)
	} else {
		f = newPromotionMigrationFixture(t, store)
	}
	f.create(t)
	f.prepare(t)
	migrations := store.(ProjectPromotionMigrationStore)
	_, err := migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, f.account.ID, f.promotion.ID)
	requirePromotionSnapshot(t, err)
	tasks := store.(AppTaskStore)
	at := time.Now().UTC().Add(time.Millisecond)
	task, err := tasks.ClaimNextAppTask(ctx, "recovery", at, time.Minute)
	requirePromotionSnapshot(t, err)
	values, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, task.AppID, task.DeploymentID)
	requirePromotionSnapshot(t, err)
	fence, err := NewRuntimeAppConfigFence(values)
	requirePromotionSnapshot(t, err)
	requirePromotionSnapshot(t, store.(ProjectMigrationTaskRuntimeStore).BindProjectMigrationTaskRuntime(ctx, task, fence))
	if _, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, at); !errors.Is(err, ErrAppTaskLeaseLost) {
		t.Fatal("unregistered migration runtime dispatched", err)
	}
	node, err := store.ComputeNodeByName(ctx, DefaultLocalNodeName)
	requirePromotionSnapshot(t, err)
	instance, err := store.(PromotionMigrationRuntimeStore).RegisterPromotionMigrationRuntime(ctx, task, node.ID)
	requirePromotionSnapshot(t, err)
	if instance == task.ID || instance == "" {
		t.Fatal("runtime ID was reused across leases")
	}
	_, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, at.Add(time.Millisecond))
	requirePromotionSnapshot(t, err)
	if len(known) > 0 && known[0] {
		exit := 23
		code, message := "nonzero_exit", "migration partially failed"
		_, err = tasks.CompleteAppTask(ctx, CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: AppTaskFailed, ExitCode: &exit, FailureCode: &code, FailureMessage: &message, FinishedAt: at.Add(2 * time.Millisecond)})
	} else {
		_, err = tasks.SweepExpiredAppTasks(ctx, at.Add(2*time.Minute))
	}
	requirePromotionSnapshot(t, err)
	h, err := migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, f.account.ID, f.promotion.ID)
	requirePromotionSnapshot(t, err)
	if h.Phase != "failed" || h.Migration.Status != "uncertain" && (len(known) == 0 || !known[0]) {
		t.Fatal("fixture has no uncertain outcome")
	}
	return f, api.ProjectPromotionMigrationRecoveryRequest{RequestID: strings.Repeat("a", 32), ExpectedTaskID: task.ID, ExpectedGeneration: h.Generation, Action: "resume", DatabaseStateChecked: true, SchemaCompatible: true, EvidenceHash: strings.Repeat("b", 64)}
}

func testPromotionRecovery(t *testing.T, factory func(*testing.T) Store) {
	testPromotionRecoveryMode(t, factory, false)
}

func testPromotionRecoveryMode(t *testing.T, factory func(*testing.T) Store, coordinated bool, workerSets ...bool) {
	scenarios := []string{"resume", "known_failure", "wrong_generation", "wrong_task", "missing_attestation", "no_teardown", "stale_teardown", "runtime_drift", "replay", "blocked_retry", "immutable", "resolve", "unknown_probe", "repair_changed"}
	if coordinated {
		scenarios = append(scenarios, "worker_missing", "worker_regressed", "worker_low_traffic", "worker_stale", "worker_binding_drift", "worker_wrong_deployment", "worker_runtime_drift")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			store := factory(t)
			f, req := promotionRecoveryFailureFixture(t, store, scenario == "known_failure", coordinated, len(workerSets) > 0 && workerSets[0])
			recoveries := store.(ProjectPromotionMigrationRecoveryStore)
			runtimes := store.(PromotionMigrationRuntimeStore)
			var repair *promotionSnapshotFixture
			if scenario == "resolve" || scenario == "unknown_probe" || scenario == "repair_changed" || strings.HasPrefix(scenario, "worker_") {
				repair = promotionRecoveryRepairFixture(t, f)
				req.Action = "resolve"
				req.SchemaCompatible = false
				req.ReplacementPromotionID = repair.promotion.ID
			}
			switch scenario {
			case "wrong_generation":
				req.ExpectedGeneration++
			case "wrong_task":
				req.ExpectedTaskID = f.sources[0].ID
			case "missing_attestation":
				req.SchemaCompatible = false
			}
			if repair != nil && coordinated {
				baselineAt := time.Now().Add(-time.Second)
				if scenario == "worker_stale" {
					baselineAt = time.Now().Add(-api.ProjectWorkerHealthBaselineWindow - time.Minute)
				}
				seedRecoveryReplacementWorkers(t, repair, baselineAt, false, 0, false)
			}
			receipt, err := recoveries.RequestProjectPromotionMigrationRecovery(ctx, f.account.ID, f.promotion.ID, req)
			if scenario == "wrong_generation" || scenario == "wrong_task" || scenario == "missing_attestation" {
				if err == nil {
					t.Fatal("invalid recovery admitted")
				}
				return
			}
			requirePromotionSnapshot(t, err)
			if scenario == "known_failure" {
				_, laneErr := store.(AppTaskStore).CreateAppTask(ctx, CreateAppTaskParams{AccountID: f.account.ID, AppID: f.apps[0].ID, DeploymentID: f.sources[0].ID, Kind: AppTaskKindRelease, Command: []string{"must not run"}})
				if laneErr == nil {
					t.Fatal("pending recovery did not reserve the migration lane")
				}
			}
			if receipt.Status != "quiescing" {
				t.Fatal("recovery skipped scheduler teardown")
			}
			if _, err = recoveries.FinalizeProjectPromotionMigrationRecovery(ctx, f.account.ID, f.promotion.ID, req.RequestID, ProjectMigrationRecoveryEvidence{}); !errors.Is(err, ErrConflict) {
				t.Fatal("recovery bypassed teardown", err)
			}
			if scenario == "no_teardown" {
				return
			}
			jobs, err := runtimes.PendingPromotionMigrationQuiescence(ctx)
			requirePromotionSnapshot(t, err)
			if len(jobs) != 1 || jobs[0].TaskID != req.ExpectedTaskID {
				t.Fatal("quiescence job missing")
			}
			if scenario == "stale_teardown" {
				wrong := jobs[0]
				wrong.LeaseToken = "stale"
				if err = runtimes.AcknowledgePromotionMigrationQuiescence(ctx, wrong); !errors.Is(err, ErrConflict) {
					t.Fatal("stale teardown accepted", err)
				}
				return
			}
			requirePromotionSnapshot(t, runtimes.AcknowledgePromotionMigrationQuiescence(ctx, jobs[0]))
			if scenario == "blocked_retry" {
				requirePromotionSnapshot(t, recoveries.BlockProjectPromotionMigrationRecovery(ctx, f.account.ID, f.promotion.ID, req.RequestID, "operator_retry"))
				h, err := store.(ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
				req.RequestID = strings.Repeat("c", 32)
				req.ExpectedGeneration = h.Generation
				receipt, err = recoveries.RequestProjectPromotionMigrationRecovery(ctx, f.account.ID, f.promotion.ID, req)
				requirePromotionSnapshot(t, err)
				jobs, err = runtimes.PendingPromotionMigrationQuiescence(ctx)
				requirePromotionSnapshot(t, err)
				requirePromotionSnapshot(t, runtimes.AcknowledgePromotionMigrationQuiescence(ctx, jobs[0]))
			}
			evidence := ProjectMigrationRecoveryEvidence{}
			if repair != nil {
				h, err := store.(ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(ctx, f.account.ID, repair.promotion.ID)
				requirePromotionSnapshot(t, err)
				evidence.StartedAt = time.Now().UTC()
				evidence.Probes = promotionHealthEvidence(h.Policy, 0, 0)
				evidence.RuntimeFingerprints = map[string]string{}
				for _, member := range repair.candidates {
					snapshot, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, member.AppID, member.DeploymentID)
					requirePromotionSnapshot(t, err)
					fence, err := NewRuntimeAppConfigFence(snapshot)
					requirePromotionSnapshot(t, err)
					evidence.RuntimeFingerprints[member.AppID] = fence.Fingerprint
				}
			}
			if scenario == "runtime_drift" || scenario == "repair_changed" {
				requirePromotionSnapshot(t, store.UpsertAppSecretInScope(ctx, f.account.ID, f.apps[0].ID, "production", "DRIFT", []byte("changed")))
			}
			if scenario == "unknown_probe" {
				evidence.Probes = nil
			}
			if repair != nil && coordinated {
				// An earlier successful promotion is insufficient: resolution must observe
				// real delivery evidence after this request and a current startup proof.
				if _, err := recoveries.FinalizeProjectPromotionMigrationRecovery(ctx, f.account.ID, f.promotion.ID, req.RequestID, evidence); !errors.Is(err, ErrPromotionWorkerNotReady) && scenario != "unknown_probe" && scenario != "repair_changed" {
					t.Fatal("resolution reused terminal worker evidence", err)
				}
				lastWorker := 1
				if len(repair.view.Workers) > 0 {
					lastWorker = len(repair.apps) - 1
				}
				if scenario != "worker_low_traffic" {
					seedRecoveryReplacementWorkers(t, repair, time.Now().Add(-time.Microsecond), scenario == "worker_regressed", lastWorker, scenario == "worker_wrong_deployment")
				}
				if scenario == "worker_missing" {
					rh, err := store.(ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(ctx, f.account.ID, repair.promotion.ID)
					requirePromotionSnapshot(t, err)
					requirePromotionSnapshot(t, store.UpdateInstanceState(ctx, recoveryReplacementWorkerInstance(rh, repair.apps[lastWorker].ID), string(StateStopped)))
				}
				if scenario == "worker_runtime_drift" {
					requirePromotionSnapshot(t, store.UpsertAppSecretInScope(ctx, f.account.ID, repair.apps[lastWorker].ID, "production", "DRIFT", []byte("changed")))
				}
				if scenario == "worker_binding_drift" {
					limit := 3
					_, err := store.(QueueBindingConsumerStore).UpdateQueueBindingWithConsumer(ctx, f.account.ID, repair.apps[lastWorker].ID, recoveryReplacementWorkerPolicy(repair, lastWorker).BindingID, UpdateQueueBindingParams{MaxConcurrency: &limit})
					requirePromotionSnapshot(t, err)
				}
			}
			result, err := recoveries.FinalizeProjectPromotionMigrationRecovery(ctx, f.account.ID, f.promotion.ID, req.RequestID, evidence)
			if scenario == "runtime_drift" || scenario == "unknown_probe" || scenario == "repair_changed" || strings.HasPrefix(scenario, "worker_") {
				if err == nil {
					t.Fatal("unverified recovery accepted")
				}
				return
			}
			requirePromotionSnapshot(t, err)
			if repair != nil {
				if result.Status != "resolved" {
					t.Fatal("repair did not resolve")
				}
				if coordinated {
					var record *promotionRecoveryRecord
					switch s := store.(type) {
					case *MemStore:
						r := s.promotionRecoveries[f.promotion.ID][req.RequestID]
						record = &r
					case *PgStore:
						record, err = readPromotionRecoveryDB(ctx, s.pool, f.account.ID, f.promotion.ID, req.RequestID)
						requirePromotionSnapshot(t, err)
					}
					if len(repair.view.Workers) > 0 {
						if record == nil || len(record.WorkerProofs) != len(repair.view.Workers) || record.WorkerProof != nil {
							t.Fatal("partial worker set recovery proof", record)
						}
						for _, proof := range record.WorkerProofs {
							if proof.Worker == nil || proof.Health.Status != "healthy" || proof.Worker.InstanceID == "" || proof.Worker.DeploymentID != proof.Worker.PreviousDeploymentID || !proof.WindowStart.Equal(receipt.RequestedAt) {
								t.Fatal("invalid set recovery proof", proof)
							}
						}
					} else if record == nil || record.WorkerProof == nil || record.WorkerProof.Worker.DeploymentID != repair.candidates[1].DeploymentID || record.WorkerProof.Worker.InstanceID == "" || record.WorkerProof.Worker.PreviousDeploymentID != repair.candidates[1].DeploymentID || record.WorkerProof.Health.Status != "healthy" || !record.WorkerProof.WindowStart.Equal(receipt.RequestedAt) {
						t.Fatal("resolution did not retain owned worker evidence", record)
					}
				}
			} else {
				if result.Status != "resumed" {
					t.Fatal("schema verification did not resume")
				}
			}
			h, err := store.(ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			if h.Migration.Status != "uncertain" && scenario != "known_failure" || h.Migration.ExitCode != nil && scenario != "known_failure" || h.Recovery == nil || h.Recovery.Status != result.Status {
				t.Fatal("recovery replaced original outcome", h)
			}
			task, err := store.(AppTaskStore).AppTaskByID(ctx, f.account.ID, f.apps[0].ID, req.ExpectedTaskID)
			requirePromotionSnapshot(t, err)
			if task.Status != AppTaskFailed || task.ExitCode != nil && scenario != "known_failure" || scenario == "known_failure" && (task.ExitCode == nil || *task.ExitCode != 23) {
				t.Fatal("task result was rewritten")
			}
			if scenario == "immutable" {
				if s, ok := store.(*PgStore); ok {
					if _, err = s.pool.Exec(ctx, "UPDATE project_environment_promotion_migration_recoveries SET payload=payload WHERE promotion_id=$1", f.promotion.ID); err == nil {
						t.Fatal("terminal recovery was mutable")
					}
				}
			}
			if scenario == "replay" || scenario == "resolve" {
				requirePromotionSnapshot(t, store.UpsertAppSecretInScope(ctx, f.account.ID, f.apps[0].ID, "production", "AFTER", []byte("changed")))
			}
			again, err := recoveries.RequestProjectPromotionMigrationRecovery(ctx, f.account.ID, f.promotion.ID, req)
			requirePromotionSnapshot(t, err)
			if again.Status != result.Status {
				t.Fatal("replay reran recovery")
			}
			changed := req
			changed.EvidenceHash = strings.Repeat("d", 64)
			if _, err = recoveries.RequestProjectPromotionMigrationRecovery(ctx, f.account.ID, f.promotion.ID, changed); !errors.Is(err, ErrConflict) {
				t.Fatal("changed acknowledgement accepted", err)
			}
			if repair != nil {
				nextTask, err := store.(AppTaskStore).CreateAppTask(ctx, CreateAppTaskParams{AccountID: f.account.ID, AppID: repair.apps[0].ID, DeploymentID: repair.candidates[0].DeploymentID, Kind: AppTaskKindRelease, Command: []string{"next explicit migration"}})
				requirePromotionSnapshot(t, err)
				claimed, err := store.(AppTaskStore).ClaimNextAppTask(ctx, "after-resolution", time.Now().UTC().Add(time.Millisecond), time.Minute)
				requirePromotionSnapshot(t, err)
				if claimed.ID != nextTask.ID || claimed.ID == req.ExpectedTaskID {
					t.Fatal("resolution did not release the migration lane for a new task")
				}
				return
			}
			if scenario == "replay" {
				return
			}
			if coordinated {
				if len(f.view.Workers) > 0 {
					seedPromotionWorkerSet(t, f, 0, 0)
				} else {
					seedPromotionWorkerRuntime(t, f, f.candidates[1].DeploymentID)
					SeedProjectWorkerJobSamplesForTest(t, f.store, f.apps[1].ID, f.account.ID, f.view.Worker.BindingID, f.previous[1].ID, time.Now().Add(-time.Second), false)
				}
				promotionWorkerRound(t, f)
				h = promotionWorkerRound(t, f)
				if h.Phase != "cutover" {
					t.Fatal("resumed coordinated graph did not qualify", h)
				}
			} else {
				h.Phase = "cutover"
				h.Verdict = "healthy"
				h.HealthyRounds = api.ProjectReleaseVerificationRounds
				now := time.Now().UTC()
				h.CheckedAt = &now
				h.Evidence = promotionHealthEvidence(h.Policy, 0, 0)
				setPromotionHealthTestCheckpoint(t, store, f.promotion.ID, h)
			}
			now := time.Now().UTC()
			_, err = store.(ProjectEnvironmentPromotionReleaseSetStore).PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
			requirePromotionSnapshot(t, err)
			if _, err = store.(AppTaskStore).ClaimNextAppTask(ctx, "no-replay", now, time.Minute); !errors.Is(err, ErrNotFound) {
				t.Fatal("recovery replayed the command", err)
			}
		})
	}
}

func promotionRecoveryRepairFixture(t *testing.T, f *promotionSnapshotFixture) *promotionSnapshotFixture {
	t.Helper()
	ctx := t.Context()
	g := *f
	g.sources = nil
	g.candidates = nil
	g.workloads = nil
	g.inputs = append([]ProjectEnvironmentPromotionWorkloadSpecInput(nil), f.inputs...)
	sourceMembers := []ProjectReleaseMember{}
	for i, app := range f.apps {
		input := f.sources[i]
		input.ID = ""
		input.BuildID = ""
		input.Status = DeployPending
		input.ReleaseCommand = nil
		input.ReleaseCommandShell = false
		input.Revision = 0
		source, err := f.store.CreateDeployment(ctx, input)
		requirePromotionSnapshot(t, err)
		requirePromotionSnapshot(t, f.store.SetDeploymentRootfs(ctx, source.ID, input.RootfsPath, input.RootfsKey, input.RootfsBytes))
		requirePromotionSnapshot(t, f.store.MarkDeploymentLive(ctx, source.ID))
		vars, err := f.store.ListAppEnvInScope(ctx, f.account.ID, app.ID, "staging")
		requirePromotionSnapshot(t, err)
		pinPromotionSnapshot(t, f.store, source.ID, vars)
		source, err = f.store.DeploymentByID(ctx, source.ID)
		requirePromotionSnapshot(t, err)
		g.sources = append(g.sources, source)
		sourceMembers = append(sourceMembers, ProjectReleaseMember{AppID: app.ID, DeploymentID: source.ID})
		g.inputs[i].SourceDeploymentID = source.ID
		g.inputs[i].PromotionID = ""
		g.workloads = append(g.workloads, ProjectEnvironmentPromotionWorkload{WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName, SourceDeploymentID: source.ID, PreviousTargetDeploymentID: f.previous[i].ID, Status: "pending"})
	}
	graph, err := f.store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "staging", 1800, sourceMembers)
	requirePromotionSnapshot(t, err)
	g.sourceGraph = graph
	g.promotion.ID = ""
	g.promotion.Status = "running"
	g.promotion.IdempotencyKey = "repair"
	g.promotion.SourceReleaseSetID = graph.ID
	g.promotion.Migrate = false
	g.promotion.Workers = len(api.ProjectReleaseWorkerPolicies(f.view.Worker, f.view.Workers)) > 0
	g.promotion.ResolveServiceBindings = true
	g.promotion.HealthPolicy = serviceGraphHealthPolicy(&g)
	g.view, err = f.store.(ProjectEnvironmentPromotionReleaseConfigurationStore).PreviewProjectEnvironmentPromotionReleaseConfiguration(ctx, g.promotion)
	requirePromotionSnapshot(t, err)
	g.promotion.ReleaseConfigurationHash = g.view.Hash
	g.promotion.HealthPolicy.Worker = copyPromotionWorker(g.view.Worker)
	g.promotion.HealthPolicy.Workers = copyPromotionWorkers(g.view.Workers)
	g.create(t)
	g.prepare(t)
	hs := f.store.(ProjectPromotionHealthStore)
	h, err := hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, g.promotion.ID)
	requirePromotionSnapshot(t, err)
	if len(g.view.Workers) > 0 {
		seedPromotionWorkerSet(t, &g, 0, 0)
		promotionWorkerRound(t, &g)
		h = promotionWorkerRound(t, &g)
	} else if g.view.Worker != nil {
		seedPromotionWorkerRuntime(t, &g, g.candidates[1].DeploymentID)
		SeedProjectWorkerJobSamplesForTest(t, f.store, g.apps[1].ID, g.account.ID, g.view.Worker.BindingID, g.previous[1].ID, time.Now().Add(-time.Second), false)
		promotionWorkerRound(t, &g)
		h = promotionWorkerRound(t, &g)
	}
	now := time.Now().UTC()
	h.Phase = "cutover"
	h.Verdict = "healthy"
	h.HealthyRounds = 2
	h.CheckedAt = &now
	h.Evidence = promotionHealthEvidence(h.Policy, 0, 0)
	setPromotionHealthTestCheckpoint(t, f.store, g.promotion.ID, h)
	_, err = f.store.(ProjectEnvironmentPromotionReleaseSetStore).PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, g.promotion.ID, 1800, g.candidates)
	requirePromotionSnapshot(t, err)
	h, err = hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, g.promotion.ID)
	requirePromotionSnapshot(t, err)
	h.Phase = "finalizing"
	h.Verdict = "healthy"
	h.HealthyRounds = 2
	h.CheckedAt = &now
	h.Evidence = promotionHealthEvidence(h.Policy, 0, 0)
	setPromotionHealthTestCheckpoint(t, f.store, g.promotion.ID, h)
	_, err = hs.AdvanceProjectEnvironmentPromotionHealth(ctx, f.account.ID, g.promotion.ID, PromotionHealthObservation{Generation: h.Generation, Action: "complete"})
	requirePromotionSnapshot(t, err)
	g.promotion, _, err = f.store.ProjectEnvironmentPromotionByID(ctx, f.account.ID, f.project.Slug, "production", g.promotion.ID)
	requirePromotionSnapshot(t, err)
	if g.promotion.Status != "succeeded" {
		t.Fatal("repair not verified", g.promotion)
	}
	return &g
}

func recoveryReplacementWorkerPolicy(f *promotionSnapshotFixture, index int) *api.ProjectReleaseWorker {
	if f.view.Worker != nil {
		return f.view.Worker
	}
	for _, worker := range f.view.Workers {
		if worker.AppID == f.apps[index].ID {
			return worker
		}
	}
	return nil
}
func recoveryReplacementWorkerInstance(h *api.ProjectPromotionHealth, app string) string {
	for _, one := range promotionWorkerHealthViews(h) {
		if one.Worker != nil && one.Worker.AppID == app {
			return one.Worker.InstanceID
		}
	}
	return ""
}
func seedRecoveryReplacementWorkers(t *testing.T, f *promotionSnapshotFixture, at time.Time, fail bool, last int, wrong bool) {
	t.Helper()
	for i, app := range f.apps {
		if app.WorkloadClass != WorkloadClassWorker {
			continue
		}
		worker := recoveryReplacementWorkerPolicy(f, i)
		dep := f.candidates[i].DeploymentID
		if wrong && i == last {
			dep = f.previous[i].ID
		}
		SeedProjectWorkerJobSamplesForTest(t, f.store, app.ID, f.account.ID, worker.BindingID, dep, at, fail && i == last)
	}
}
