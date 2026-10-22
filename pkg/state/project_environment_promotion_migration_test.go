package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionMigrationMemory(t *testing.T) {
	testPromotionMigration(t, func(*testing.T) Store { return NewMemStore() })
}

func newPromotionMigrationFixture(t *testing.T, store Store) *promotionSnapshotFixture {
	t.Helper()
	f := newPromotionSnapshotFixture(t, store, false, true, true)
	f.promotion.HealthPolicy = serviceGraphHealthPolicy(f)
	migration := *f.view.Migration
	f.promotion.HealthPolicy.Migration = &migration
	return f
}

func testPromotionMigration(t *testing.T, factory func(*testing.T) Store) {
	testPromotionMigrationMode(t, factory, false)
}

func testPromotionMigrationMode(t *testing.T, factory func(*testing.T) Store, coordinated bool) {
	for _, scenario := range []string{"success", "failed", "uncertain", "restore_reclaim", "credential_drift", "publish_drift", "missing_attestation", "competing_admission", "wrong_policy", "not_opted", "start_deadline", "task_immutable"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			var f *promotionSnapshotFixture
			if coordinated {
				f = newCoordinatedPromotionFixture(t, factory(t))
			} else {
				f = newPromotionMigrationFixture(t, factory(t))
			}
			if scenario == "wrong_policy" {
				f.promotion.HealthPolicy.Migration.CommandHash = api.ProjectReleaseCommandHash([]string{"other"}, true)
			}
			if scenario == "not_opted" {
				f.promotion.Migrate = false
			}
			if scenario == "wrong_policy" || scenario == "not_opted" {
				_, _, err := f.store.CreateProjectEnvironmentPromotion(ctx, f.promotion, f.workloads)
				if err == nil {
					t.Fatal("unapproved command admitted")
				}
				return
			}
			f.create(t)
			f.prepare(t)
			migrations := f.store.(ProjectPromotionMigrationStore)
			health := f.store.(ProjectPromotionHealthStore)
			tasks := f.store.(AppTaskStore)
			h, err := health.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			if coordinated {
				assertCoordinatedWorkerDormant(t, f)
			}
			if h.Phase != "migrating" || h.Migration.Status != "waiting" {
				t.Fatal("migration not first phase")
			}
			publisher := f.store.(ProjectEnvironmentPromotionReleaseSetStore)
			if _, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates); !errors.Is(err, ErrConflict) && (!coordinated || !errors.Is(err, ErrPromotionWorkerNotReady)) {
				t.Fatal("published before migration", err)
			}
			if scenario == "start_deadline" {
				h.StartedAt = time.Now().Add(-2 * api.ProjectPromotionMigrationStartTimeout)
				setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
				h, err = migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
				persisted, readErr := health.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, readErr)
				if persisted.Phase != "failed" || persisted.Generation <= 1 {
					t.Fatal("admission deadline failure was not persisted")
				}
				if h.Phase != "failed" || h.Migration.TaskID != "" {
					t.Fatal("late admission created a task", h)
				}
				return
			}
			h, err = migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			taskID := h.Migration.TaskID
			if taskID == "" || h.Migration.Status != "queued" {
				t.Fatalf("no queued task: %+v", h)
			}
			// Repeat reconciliation represents a lost response and a restarted worker.
			queuedGeneration := h.Generation
			for range 2 {
				h, err = migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
				if h.Migration.TaskID != taskID || h.Generation != queuedGeneration {
					t.Fatal("replayed task or changed an unchanged checkpoint")
				}
			}
			if scenario == "competing_admission" {
				other := f.promotion
				other.ID = ""
				other.IdempotencyKey = "another"
				other.Migrate = true
				other.Workers = coordinated
				other.ResolveServiceBindings = true
				other.HealthPolicy = &h.Policy
				if _, _, err = f.store.CreateProjectEnvironmentPromotion(ctx, other, f.workloads); !errors.Is(err, ErrConflict) {
					t.Fatal("concurrent migration admitted", err)
				}
				_, err = tasks.CreateAppTask(ctx, CreateAppTaskParams{AccountID: f.account.ID, AppID: f.apps[1].ID, DeploymentID: f.candidates[1].DeploymentID, Kind: AppTaskKindRelease, Command: []string{"other"}})
				if err == nil {
					t.Fatal("ordinary release task bypassed reservation")
				}
				return
			}
			if scenario == "task_immutable" {
				assertPromotionMigrationImmutable(t, f)
				return
			}
			at := time.Now().UTC().Add(time.Millisecond)
			leaseDuration := time.Minute
			if scenario == "restore_reclaim" {
				leaseDuration = time.Second
			}
			task, err := tasks.ClaimNextAppTask(ctx, "migration-test", at, leaseDuration)
			requirePromotionSnapshot(t, err)
			if task.ID != taskID || task.DeploymentScope != "production" || task.RetryMax != 0 || task.Command[0] != "database expand --compatible" || task.ArtifactKey != f.sources[0].RootfsKey {
				t.Fatalf("wrong destination task: %+v", task)
			}
			if scenario == "missing_attestation" {
				assertPromotionMigrationLegacyDispatchBlocked(t, f, task)
				if _, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, at); !errors.Is(err, ErrAppTaskLeaseLost) {
					t.Fatal("unattested task dispatched", err)
				}
				return
			}
			snapshot, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account.ID, task.AppID, task.DeploymentID)
			requirePromotionSnapshot(t, err)
			fence, err := NewRuntimeAppConfigFence(snapshot)
			requirePromotionSnapshot(t, err)
			runtime := f.store.(ProjectMigrationTaskRuntimeStore)
			requirePromotionSnapshot(t, runtime.BindProjectMigrationTaskRuntime(ctx, task, fence))
			node, nodeErr := f.store.ComputeNodeByName(ctx, DefaultLocalNodeName)
			requirePromotionSnapshot(t, nodeErr)
			_, nodeErr = f.store.(PromotionMigrationRuntimeStore).RegisterPromotionMigrationRuntime(ctx, task, node.ID)
			requirePromotionSnapshot(t, nodeErr)
			if scenario == "restore_reclaim" {
				old := task
				_, err = tasks.SweepExpiredAppTasks(ctx, at.Add(2*time.Second))
				requirePromotionSnapshot(t, err)
				// Keep the restored task inside its start window without waiting for wall time.
				// Sweeping a lost restoring lease is safe because no dispatch occurred.
				task, err = tasks.ClaimNextAppTask(ctx, "replacement", at.Add(3*time.Second), time.Minute)
				requirePromotionSnapshot(t, err)
				if task.ID != old.ID || *task.LeaseToken == *old.LeaseToken {
					t.Fatal("restoring replay changed identity")
				}
				if _, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, at.Add(4*time.Second)); !errors.Is(err, ErrAppTaskLeaseLost) {
					t.Fatal("old attestation authorized new lease")
				}
				requirePromotionSnapshot(t, runtime.BindProjectMigrationTaskRuntime(ctx, task, fence))
				_, nodeErr = f.store.(PromotionMigrationRuntimeStore).RegisterPromotionMigrationRuntime(ctx, task, node.ID)
				requirePromotionSnapshot(t, nodeErr)
				at = at.Add(5 * time.Second)
			}
			if scenario == "credential_drift" {
				requirePromotionSnapshot(t, f.store.UpsertAppSecretWithClassInScope(ctx, f.account.ID, task.AppID, "production", "TOKEN", f.identity.Recipient().String(), "0123456789abcdef", api.SecretClassPersistent, []byte("rotated-envelope")))
				if _, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, at.Add(5*time.Millisecond)); !errors.Is(err, ErrAppTaskLeaseLost) {
					t.Fatal("stale destination runtime dispatched", err)
				}
				return
			}
			task, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, at.Add(5*time.Millisecond))
			requirePromotionSnapshot(t, err)
			exit := 0
			status := AppTaskSucceeded
			if scenario == "failed" {
				exit = 2
				status = AppTaskFailed
			}
			if scenario == "uncertain" {
				_, err = tasks.SweepExpiredAppTasks(ctx, at.Add(2*time.Minute))
				requirePromotionSnapshot(t, err)
			} else {
				params := CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: status, ExitCode: &exit, FinishedAt: at.Add(10 * time.Millisecond)}
				if status == AppTaskFailed {
					code, message := "nonzero_exit", "migration failed"
					params.FailureCode = &code
					params.FailureMessage = &message
				}
				_, err = tasks.CompleteAppTask(ctx, params)
				requirePromotionSnapshot(t, err)
			}
			h, err = migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			if scenario == "failed" || scenario == "uncertain" {
				if coordinated {
					assertCoordinatedWorkerDormant(t, f)
				}
				if h.Phase != "failed" || scenario == "uncertain" && h.Migration.Status != "uncertain" {
					t.Fatalf("unproven migration passed: %+v", h)
				}
				if _, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates); err == nil {
					t.Fatal("failed migration published")
				}
				h, err = migrations.ReconcileProjectEnvironmentPromotionMigration(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
				if h.Migration.TaskID != taskID {
					t.Fatal("failure replayed")
				}
				if scenario == "uncertain" {
					other := f.promotion
					other.ID = ""
					other.IdempotencyKey = "after-unknown"
					other.Migrate = true
					other.Workers = coordinated
					other.ResolveServiceBindings = true
					other.HealthPolicy = &h.Policy
					if _, _, err = f.store.CreateProjectEnvironmentPromotion(ctx, other, f.workloads); !errors.Is(err, ErrConflict) {
						t.Fatal("new migration bypassed an uncertain outcome", err)
					}
				}
				return
			}
			if h.Phase != "preflight" || h.Migration.Status != "succeeded" || h.Migration.ExitCode == nil || *h.Migration.ExitCode != 0 {
				t.Fatalf("success lacks receipt: %+v", h)
			}
			if coordinated {
				seedPromotionWorkerRuntime(t, f, f.candidates[1].DeploymentID)
				SeedProjectWorkerJobSamplesForTest(t, f.store, f.apps[1].ID, f.account.ID, f.view.Worker.BindingID, f.previous[1].ID, time.Now().Add(-time.Second), false)
				promotionWorkerRound(t, f)
				h = promotionWorkerRound(t, f)
				if h.Phase != "cutover" {
					t.Fatal("coordinated preflight did not qualify", h)
				}
			} else {
				h.Phase = "cutover"
				h.Verdict = "healthy"
				h.HealthyRounds = api.ProjectReleaseVerificationRounds
				now := time.Now().UTC()
				h.CheckedAt = &now
				h.Evidence = promotionHealthEvidence(h.Policy, 0, 0)
				setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
			}
			if scenario == "publish_drift" {
				requirePromotionSnapshot(t, f.store.UpsertAppSecretWithClassInScope(ctx, f.account.ID, task.AppID, "production", "TOKEN", f.identity.Recipient().String(), "0123456789abcdef", api.SecretClassPersistent, []byte("rotated-envelope")))
				if _, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates); !errors.Is(err, ErrConflict) {
					t.Fatal("cutover accepted runtime drift", err)
				}
				return
			}
			_, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
			requirePromotionSnapshot(t, err)
		})
	}
}

func assertPromotionMigrationImmutable(t *testing.T, f *promotionSnapshotFixture) {
	t.Helper()
	if s, ok := f.store.(*PgStore); ok {
		for _, stmt := range []string{"UPDATE project_environment_promotion_migrations SET task_id=NULL,runtime_fingerprint='' WHERE promotion_id=$1", "DELETE FROM project_environment_promotion_migrations WHERE promotion_id=$1"} {
			if _, err := s.pool.Exec(t.Context(), stmt, f.promotion.ID); err == nil {
				t.Fatal("migration journal was mutable")
			}
		}
	}
}

func assertPromotionMigrationLegacyDispatchBlocked(t *testing.T, f *promotionSnapshotFixture, task AppTask) {
	t.Helper()
	if s, ok := f.store.(*PgStore); ok {
		_, err := s.pool.Exec(t.Context(), "UPDATE app_tasks SET status='running',started_at=now(),attempt_count=attempt_count+1 WHERE id=$1", task.ID)
		if err == nil || !strings.Contains(err.Error(), "not authorized for dispatch") {
			t.Fatal("legacy dispatcher bypassed runtime attestation", err)
		}
	}
}
