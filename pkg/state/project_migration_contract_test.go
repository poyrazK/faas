package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func editContractFixture(t *testing.T, f canaryStoreFixture, edit func(*api.ProjectSubmissionReceipt), processing bool) {
	t.Helper()
	r, err := f.store.(ProjectSubmissionStore).ProjectSubmission(t.Context(), f.account, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	edit(&r)
	raw, _ := json.Marshal(r)
	status := "complete"
	if processing {
		status = "processing"
	}
	switch s := f.store.(type) {
	case *MemStore:
		s.mu.Lock()
		entry := s.projectSubmissions[f.account+"\x00"+f.attempt]
		entry.Payload = raw
		entry.Status = status
		s.projectSubmissions[f.account+"\x00"+f.attempt] = entry
		s.mu.Unlock()
	case *PgStore:
		if _, err = s.pool.Exec(t.Context(), "UPDATE project_submissions SET payload=$3::jsonb,status=$4 WHERE account_id=$1 AND attempt_id=$2", f.account, f.attempt, raw, status); err != nil {
			t.Fatal(err)
		}
	}
}
func newContractFixture(t *testing.T, store Store) canaryStoreFixture {
	t.Helper()
	f := newCanaryStoreFixture(t, store)
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		for id, dep := range s.deployments {
			if s.apps[dep.AppID].ProjectID == f.project {
				dep.ImageDigest = "sha256:contract-fixture"
				s.deployments[id] = dep
			}
		}
		s.mu.Unlock()
	case *PgStore:
		if _, err := s.pool.Exec(t.Context(), "UPDATE deployments SET image_digest='sha256:contract-fixture' WHERE app_id IN (SELECT id FROM apps WHERE project_id=$1)", f.project); err != nil {
			t.Fatal(err)
		}
	}

	editContractFixture(t, f, func(r *api.ProjectSubmissionReceipt) {
		r.Apply.SafeRelease.ReleaseCommand = &api.ProjectReleaseCommand{AppID: f.apps[0].ID, Slug: f.apps[0].Slug, Compatibility: "backward-compatible", Status: "succeeded", VerifyBaseline: true, CommandHash: api.ProjectReleaseCommandHash([]string{"expand"}, true), ContractCommandHash: api.ProjectReleaseCommandHash([]string{"contract-private-command"}, true)}
	}, true)
	contracts := store.(ProjectMigrationContractStore)
	if err := contracts.StageProjectMigrationContract(t.Context(), f.account, f.attempt, f.project, "different-command"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed source accepted: %v", err)
	}
	if err := contracts.StageProjectMigrationContract(t.Context(), f.account, f.attempt, f.project, "contract-private-command"); err != nil {
		t.Fatal(err)
	}
	// Stand in for the scheduler's prepared expansion VM, without executing SQL.
	receipt, err := store.(ProjectSubmissionStore).ProjectSubmission(t.Context(), f.account, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	deployment := ""
	for _, b := range receipt.Apply.Builds {
		if b.AppID == f.apps[0].ID {
			deployment = b.DeploymentID
		}
	}
	tasks := store.(AppTaskStore)
	task, err := tasks.CreateAppTask(t.Context(), CreateAppTaskParams{AccountID: f.account, AppID: f.apps[0].ID, DeploymentID: deployment, Kind: AppTaskKindRelease, Command: []string{"expand"}, CommandShell: true})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(time.Millisecond)
	task, err = tasks.ClaimNextAppTask(t.Context(), "expand", at, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(t.Context(), f.account, f.apps[0].ID, deployment)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.(ProjectMigrationTaskRuntimeStore).BindProjectMigrationTaskRuntime(t.Context(), task, fence); err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.MarkAppTaskRunning(t.Context(), task.ID, *task.LeaseToken, at.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if _, err = tasks.CompleteAppTask(t.Context(), CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: AppTaskSucceeded, ExitCode: &zero, FinishedAt: at.Add(2 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	editContractFixture(t, f, func(r *api.ProjectSubmissionReceipt) { r.Apply.SafeRelease.ReleaseCommand.TaskID = task.ID }, false)

	enableStabilizationFixture(t, f)
	for range 6 {
		f.advance(t, false)
	}
	f.advance(t, false)
	expireCanaryControl(t, f)
	if _, err := store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, nil); err != nil {
		t.Fatal(err)
	}
	editContractFixture(t, f, func(r *api.ProjectSubmissionReceipt) {
		past := time.Now().UTC().Add(-time.Minute)
		r.Apply.SafeRelease.Verification.Stabilization.Deadline = &past
	}, false)
	safe := f.progress(t)
	if safe.Verification.Stabilization.Status != "completed" || safe.Verification.Stabilization.Health != "healthy" {
		t.Fatalf("fixture not stable: %+v", safe.Verification.Stabilization)
	}
	return f
}
func expireContractRetention(t *testing.T, f canaryStoreFixture) {
	t.Helper()
	current, err := f.store.(ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), f.account, f.project, "production")
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range current.Members {
		if _, err = f.store.UpdateDeploymentTraffic(t.Context(), member.DeploymentID, 100); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Minute)
	switch s := f.store.(type) {
	case *MemStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		for key, r := range s.projectReleaseSets {
			if r.ProjectID == f.project && !r.Active {
				r.ExpiresAt = &past
				s.projectReleaseSets[key] = r
			}
		}
		for id := range s.revisionPins {
			s.revisionPins[id] = past
		}
	case *PgStore:
		if _, err := s.pool.Exec(t.Context(), "UPDATE project_release_sets SET expires_at=$2 WHERE project_id=$1 AND NOT active", f.project, past); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(t.Context(), "UPDATE deployment_revision_pins SET expires_at=$1", past); err != nil {
			t.Fatal(err)
		}
	}
}
func contractEvidence(f canaryStoreFixture, t *testing.T) (api.ProjectReleaseControlRequest, ProjectMigrationContractEvidence) {
	t.Helper()
	safe := f.progress(t)
	evidence := ProjectMigrationContractEvidence{StartedAt: time.Now().UTC()}
	receipt, err := f.store.(ProjectSubmissionStore).ProjectSubmission(t.Context(), f.account, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	deployment := ""
	for _, b := range receipt.Apply.Builds {
		if b.AppID == f.apps[0].ID {
			deployment = b.DeploymentID
		}
	}
	snapshot, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(t.Context(), f.account, f.apps[0].ID, deployment)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	evidence.RuntimeFingerprint = fence.Fingerprint

	for _, p := range safe.Verification.Probes {
		evidence.Evidence = append(evidence.Evidence, api.ProjectReleaseProbeEvidence{Probe: p, StableRequests: api.RouteHealthProbeRequestsPerMinute})
	}
	return api.ProjectReleaseControlRequest{RequestID: strings.Repeat("d", 32), ExpectedGeneration: safe.Verification.Generation}, evidence
}
func TestMigrationContractMemory(t *testing.T) {
	testMigrationContract(t, func(*testing.T) Store { return NewMemStore() })
}
func testMigrationContract(t *testing.T, factory func(*testing.T) Store) {
	for _, outcome := range []string{"success", "uncertain", "failed", "unknown_health", "runtime_changed", "stale_health", "wrong_generation", "new_graph", "new_config", "retention", "old_instance", "queued_work", "unknown_stabilization", "atomic_write_failure"} {
		t.Run(outcome, func(t *testing.T) {
			store := factory(t)
			if outcome == "atomic_write_failure" {
				if _, ok := store.(*PgStore); !ok {
					t.Skip("PostgreSQL transaction injection")
				}
			}
			f := newContractFixture(t, store)
			ctx := t.Context()
			contracts := store.(ProjectMigrationContractStore)
			if outcome != "retention" {
				expireContractRetention(t, f)
			}
			req, evidence := contractEvidence(f, t)
			switch outcome {
			case "unknown_health":
				evidence.Evidence = nil
			case "stale_health":
				evidence.StartedAt = time.Now().Add(-2 * api.ProjectMigrationBaselineEvidenceMaxAge)
			case "runtime_changed":
				if err := store.(interface {
					UpsertAppSecretInScope(context.Context, string, string, string, string, []byte) error
				}).UpsertAppSecretInScope(ctx, f.account, f.apps[0].ID, "production", "DATABASE_CREDENTIAL", []byte("sealed-v2")); err != nil {
					t.Fatal(err)
				}

			case "wrong_generation":
				req.ExpectedGeneration++
			case "new_graph":
				current, _ := store.(ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, f.account, f.project, "production")
				if _, err := store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account, f.project, "production", 1800, current.Members); err != nil {
					t.Fatal(err)
				}
			case "new_config":
				if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, ProjectEnvironmentConfig{AccountID: f.account, ProjectID: f.project, EnvironmentSlug: "production", ConfigHash: f.previous.ConfigHash, Values: f.previous.Values}); err != nil {
					t.Fatal(err)
				}
			case "unknown_stabilization":
				editContractFixture(t, f, func(r *api.ProjectSubmissionReceipt) {
					r.Apply.SafeRelease.Verification.Stabilization.Health = "unknown"
				}, false)
			case "old_instance":
				node, err := store.ComputeNodeByName(ctx, DefaultLocalNodeName)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.CreateInstance(ctx, f.apps[0].ID, f.old[0].DeploymentID, "cold_booting", 256, node.ID, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			case "queued_work":
				safe := f.progress(t)
				dep := safe.ReleaseID
				current, _ := store.(ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, f.account, f.project, "production")
				for _, m := range current.Members {
					if m.AppID == f.apps[0].ID {
						dep = m.DeploymentID
					}
				}
				if _, err := store.(AppTaskStore).CreateAppTask(ctx, CreateAppTaskParams{AccountID: f.account, AppID: f.apps[0].ID, DeploymentID: dep, Kind: AppTaskKindManual, Command: []string{"other"}}); err != nil {
					t.Fatal(err)
				}
			case "atomic_write_failure":
				pg := store.(*PgStore)
				if _, err := pg.pool.Exec(ctx, `CREATE FUNCTION reject_contract_admission() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.task_id IS NOT NULL THEN RAISE EXCEPTION 'injected contract write failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_contract_admission BEFORE UPDATE ON project_migration_contracts FOR EACH ROW EXECUTE FUNCTION reject_contract_admission()`); err != nil {
					t.Fatal(err)
				}
			}
			result, err := contracts.AdmitProjectMigrationContract(ctx, f.account, f.attempt, req, evidence)
			success := outcome == "success" || outcome == "uncertain" || outcome == "failed"
			if !success {
				if err == nil {
					t.Fatalf("unsafe cleanup admitted: %+v", result)
				}
				retired, _ := store.(ProjectMigrationFenceReader).ProjectMigrationDeploymentRetired(ctx, f.old[0].DeploymentID)
				if retired {
					t.Fatal("rejected admission changed retirement fence")
				}
				if outcome == "atomic_write_failure" {
					pg := store.(*PgStore)
					var count int
					if err = pg.pool.QueryRow(ctx, "SELECT count(*) FROM app_tasks WHERE kind='manual'").Scan(&count); err != nil || count != 0 {
						t.Fatalf("failed transaction left a task: %d %v", count, err)
					}
					if _, err = pg.pool.Exec(ctx, "DROP TRIGGER reject_contract_admission ON project_migration_contracts; DROP FUNCTION reject_contract_admission()"); err != nil {
						t.Fatal(err)
					}
					if _, err = contracts.AdmitProjectMigrationContract(ctx, f.account, f.attempt, req, evidence); err != nil {
						t.Fatalf("recovery failed: %v", err)
					}
				}
				return
			}
			if err != nil || result.TaskID == "" || result.Status != "queued" || result.RetiredDeployments != len(f.old) {
				view, _ := contracts.ProjectMigrationContractStatus(ctx, f.account, f.attempt)
				t.Fatalf("admission: %+v %v status=%+v", result, err, view)
			}
			if pg, ok := store.(*PgStore); ok {
				store = NewPgStore(pg.pool.Pool)
				contracts = store.(ProjectMigrationContractStore)
			}
			replay, err := contracts.AdmitProjectMigrationContract(ctx, f.account, f.attempt, req, ProjectMigrationContractEvidence{})
			if err != nil || replay.TaskID != result.TaskID {
				t.Fatalf("restart/lost response replay: %+v %v", replay, err)
			}
			other := req
			other.RequestID = strings.Repeat("e", 32)
			if _, err = contracts.AdmitProjectMigrationContract(ctx, f.account, f.attempt, other, evidence); !errors.Is(err, ErrConflict) {
				t.Fatalf("second cleanup admitted: %v", err)
			}
			if _, err = store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account, f.project, "production", 1800, f.old); err == nil {
				t.Fatal("old code republished after retirement")
			}
			old, _ := store.DeploymentByID(ctx, f.old[0].DeploymentID)
			if _, err = ResolveAppForDeployment(ctx, store, f.apps[0], old); err == nil {
				t.Fatal("retired runtime projection succeeded")
			}
			node, _ := store.ComputeNodeByName(ctx, DefaultLocalNodeName)
			parked, err := store.CreateInstance(ctx, f.apps[0].ID, f.old[0].DeploymentID, "parked", 256, node.ID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if err = store.UpdateInstanceState(ctx, parked.ID, "waking"); err == nil {
				t.Fatal("old parked instance woke after contract admission")
			}
			tasks := store.(AppTaskStore)
			now := time.Now().UTC().Add(time.Second)
			claimed, err := tasks.ClaimNextAppTask(ctx, "contract", now, time.Minute)
			if err != nil || claimed.ID != result.TaskID {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			if _, err = tasks.MarkAppTaskRunning(ctx, claimed.ID, *claimed.LeaseToken, now.Add(time.Second)); err == nil {
				t.Fatal("cleanup dispatched without a prepared runtime attestation")
			}
			snapshot, err := store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account, claimed.AppID, claimed.DeploymentID)
			if err != nil {
				t.Fatal(err)
			}
			fence, err := NewRuntimeAppConfigFence(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.(ProjectMigrationTaskRuntimeStore).BindProjectMigrationTaskRuntime(ctx, claimed, fence); err != nil {
				t.Fatal(err)
			}
			running, err := tasks.MarkAppTaskRunning(ctx, claimed.ID, *claimed.LeaseToken, now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			status := AppTaskSucceeded
			exit := 0
			params := CompleteAppTaskParams{ID: running.ID, LeaseToken: *running.LeaseToken, Status: status, ExitCode: &exit, FinishedAt: now.Add(2 * time.Second)}
			if outcome == "failed" {
				params.Status = AppTaskFailed
				exit = 1
				code, message := "command_failed", "contract exited nonzero"
				params.FailureCode = &code
				params.FailureMessage = &message
			}
			if outcome == "uncertain" {
				params.Status = AppTaskFailed
				params.ExitCode = nil
				code, message := "execution_transport_failed", "result lost"
				params.FailureCode = &code
				params.FailureMessage = &message
			}
			if _, err = tasks.CompleteAppTask(ctx, params); err != nil {
				t.Fatal(err)
			}
			view, err := contracts.ProjectMigrationContractStatus(ctx, f.account, f.attempt)
			if err != nil {
				t.Fatal(err)
			}
			want := "succeeded"
			if outcome == "failed" {
				want = "failed"
			}
			if outcome == "uncertain" {
				want = "uncertain"
			}
			if view.Status != want {
				t.Fatalf("result=%+v want=%s", view, want)
			}
			if _, err = tasks.ClaimNextAppTask(ctx, "replay", now.Add(3*time.Second), time.Minute); !errors.Is(err, ErrNotFound) {
				t.Fatalf("cleanup replayed after execution: %v", err)
			}
			retired, err := store.(ProjectMigrationFenceReader).ProjectMigrationDeploymentRetired(ctx, old.ID)
			if err != nil || !retired {
				t.Fatal("terminal outcome removed retirement fence")
			}
			if _, err = store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account, f.project, "production", 1800, f.old); err == nil {
				t.Fatal("terminal contract allowed rollback to retired code")
			}
			receipt, err := store.(ProjectSubmissionStore).ProjectSubmission(ctx, f.account, f.attempt)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(receipt)
			if strings.Contains(string(raw), "contract-private-command") {
				t.Fatal("cleanup command leaked into public receipt")
			}
		})
	}
}
