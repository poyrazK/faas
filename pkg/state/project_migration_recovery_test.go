package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMigrationRecoveryMemory(t *testing.T) {
	testMigrationRecovery(t, func(*testing.T) Store { return NewMemStore() })
}
func testMigrationRecovery(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"success", "uncertain", "wrong_task", "wrong_generation", "unverified", "stale_health", "unknown_health", "runtime_changed", "retention", "missing_task", "write_failure"} {
		t.Run(scenario, func(t *testing.T) {
			store := factory(t)
			if scenario == "write_failure" {
				if _, ok := store.(*PgStore); !ok {
					t.Skip("PostgreSQL transaction failure injection")
				}
			}
			f := newContractFixture(t, store)
			expireContractRetention(t, f)
			ctx := t.Context()
			request, proof := contractEvidence(f, t)
			contract, err := store.(ProjectMigrationContractStore).AdmitProjectMigrationContract(ctx, f.account, f.attempt, request, proof)
			if err != nil {
				t.Fatal(err)
			}
			tasks := store.(AppTaskStore)
			at := time.Now().UTC().Add(time.Millisecond)
			task, err := tasks.ClaimNextAppTask(ctx, "cleanup", at, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			values, err := store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account, task.AppID, task.DeploymentID)
			if err != nil {
				t.Fatal(err)
			}
			fence, _ := NewRuntimeAppConfigFence(values)
			if err = store.(ProjectMigrationTaskRuntimeStore).BindProjectMigrationTaskRuntime(ctx, task, fence); err != nil {
				t.Fatal(err)
			}
			if _, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, at); err != nil {
				t.Fatal(err)
			}
			exit := 23
			code, message := "command_failed", "cleanup failure"
			completion := CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: AppTaskFailed, ExitCode: &exit, FailureCode: &code, FailureMessage: &message, FinishedAt: at.Add(time.Millisecond)}
			if scenario == "uncertain" {
				completion.ExitCode = nil
			}
			if _, err = tasks.CompleteAppTask(ctx, completion); err != nil {
				t.Fatal(err)
			}
			old, _ := store.(ProjectSubmissionStore).ProjectSubmission(ctx, f.account, f.attempt)
			replacement := strings.Repeat("e", 32)
			r := old
			r.AttemptID = replacement
			r.Status = "complete"
			r.Apply.Builds = nil
			r.Apply.SafeRelease = &api.ProjectSafeRelease{Status: "succeeded", TTLSeconds: 1800, Configuration: old.Apply.SafeRelease.Configuration, Verification: &api.ProjectReleaseVerification{Probes: old.Apply.SafeRelease.Verification.Probes, HealthyRounds: api.ProjectReleaseVerificationRounds, CheckedAt: &at}}
			members := []ProjectReleaseMember{}
			for _, app := range f.apps {
				dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball, ImageDigest: "sha256:repair-fixture"})
				if err != nil {
					t.Fatal(err)
				}
				if err = store.SetDeploymentRootfs(ctx, dep.ID, "/repair.ext4", "layers/"+dep.ID, 4096); err != nil {
					t.Fatal(err)
				}
				if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
					t.Fatal(err)
				}
				members = append(members, ProjectReleaseMember{AppID: app.ID, DeploymentID: dep.ID})
				r.Apply.Builds = append(r.Apply.Builds, api.AppliedBuild{AppID: app.ID, Slug: app.Slug, DeploymentID: dep.ID})
			}
			graph, err := store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account, f.project, "production", 1800, members)
			if err != nil {
				t.Fatal(err)
			}
			r.Apply.SafeRelease.ReleaseID = graph.ID
			subs := store.(ProjectSubmissionStore)
			if _, err = subs.ReserveProjectSubmission(ctx, f.account, replacement, strings.Repeat("f", 64), r); err != nil {
				t.Fatal(err)
			}
			if err = subs.WriteProjectSubmission(ctx, f.account, r); err != nil {
				t.Fatal(err)
			}
			req := api.ProjectMigrationRecoveryRequest{RequestID: strings.Repeat("b", 32), ExpectedTaskID: contract.TaskID, ExpectedGeneration: contract.ExpectedGeneration, ReplacementAttemptID: replacement, DatabaseStateChecked: true, ExecutionQuiescent: true, EvidenceHash: strings.Repeat("a", 64)}
			evidence := ProjectMigrationRecoveryEvidence{StartedAt: time.Now().UTC(), RuntimeFingerprints: map[string]string{}}
			for _, build := range r.Apply.Builds {
				snapshot, err := store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account, build.AppID, build.DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				fp, err := NewRuntimeAppConfigFence(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				evidence.RuntimeFingerprints[build.AppID] = fp.Fingerprint
			}
			for _, p := range r.Apply.SafeRelease.Verification.Probes {
				evidence.Probes = append(evidence.Probes, api.ProjectReleaseProbeEvidence{Probe: p, StableRequests: api.RouteHealthProbeRequestsPerMinute, CandidateRequests: api.RouteHealthProbeRequestsPerMinute})
			}
			if scenario != "retention" {
				drainRecoveryFixture(t, f, r)
			}
			switch scenario {
			case "wrong_task":
				req.ExpectedTaskID = uuid.NewString()
			case "wrong_generation":
				req.ExpectedGeneration++
			case "unverified":
				r.Apply.SafeRelease.Verification.HealthyRounds = 0
				editRecoveryReceipt(t, store, f.account, r)
			case "stale_health":
				evidence.StartedAt = time.Now().Add(-time.Hour)
			case "unknown_health":
				evidence.Probes = nil
			case "runtime_changed":
				if err = store.UpsertAppSecretInScope(ctx, f.account, f.apps[0].ID, "production", "LATE", []byte("changed")); err != nil {
					t.Fatal(err)
				}
			case "missing_task":
				deleteRecoveryTaskFixture(t, store, task.ID)
			}
			if scenario == "write_failure" {
				pg := store.(*PgStore)
				if _, err = pg.pool.Exec(ctx, `CREATE FUNCTION reject_recovery_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture'; END $$; CREATE TRIGGER reject_recovery_fixture BEFORE INSERT ON project_migration_recoveries FOR EACH ROW EXECUTE FUNCTION reject_recovery_fixture()`); err != nil {
					t.Fatal(err)
				}
				if _, err = store.(ProjectMigrationRecoveryStore).ResolveProjectMigrationContract(ctx, f.account, f.attempt, req, evidence); err == nil {
					t.Fatal("injected write failure accepted")
				}
				for _, build := range old.Apply.Builds {
					retired, err := store.(ProjectMigrationFenceReader).ProjectMigrationDeploymentRetired(ctx, build.DeploymentID)
					if err != nil || retired {
						t.Fatal("failed resolution left retirement effects")
					}
				}
				if _, err = pg.pool.Exec(ctx, `DROP TRIGGER reject_recovery_fixture ON project_migration_recoveries; DROP FUNCTION reject_recovery_fixture()`); err != nil {
					t.Fatal(err)
				}
				scenario = "success"
			}
			result, err := store.(ProjectMigrationRecoveryStore).ResolveProjectMigrationContract(ctx, f.account, f.attempt, req, evidence)
			if scenario != "success" && scenario != "uncertain" {
				if err == nil {
					t.Fatalf("unsafe resolution accepted: %+v", result)
				}
				return
			}
			if err != nil || result.Status != "resolved" || result.TaskID != task.ID {
				t.Fatalf("resolve: %+v %v", result, err)
			}
			status, err := store.(ProjectMigrationContractStore).ProjectMigrationContractStatus(ctx, f.account, f.attempt)
			if err != nil || status.Recovery == nil || status.Recovery.Status != "resolved" {
				t.Fatalf("durable recovery: %+v %v", status, err)
			}
			if scenario == "uncertain" && status.Status != "uncertain" {
				t.Fatal("resolution rewrote unknown exit")
			}
			replay, err := store.(ProjectMigrationRecoveryStore).ResolveProjectMigrationContract(ctx, f.account, f.attempt, req, ProjectMigrationRecoveryEvidence{})
			if err != nil || !replay.ResolvedAt.Equal(*result.ResolvedAt) {
				t.Fatal("lost response changed resolution")
			}
			other := req
			other.EvidenceHash = strings.Repeat("c", 64)
			if _, err = store.(ProjectMigrationRecoveryStore).ResolveProjectMigrationContract(ctx, f.account, f.attempt, other, evidence); !errors.Is(err, ErrConflict) {
				t.Fatal("changed resolution accepted")
			}
			if _, err = store.(ProjectMigrationContractStore).AdmitProjectMigrationContract(ctx, f.account, f.attempt, request, ProjectMigrationContractEvidence{}); err != nil {
				t.Fatal("cleanup replay receipt unavailable")
			}
			if _, err = tasks.ClaimNextAppTask(ctx, "again", time.Now().UTC(), time.Minute); !errors.Is(err, ErrNotFound) {
				t.Fatal("cleanup replayed")
			}
			for _, build := range old.Apply.Builds {
				retired, err := store.(ProjectMigrationFenceReader).ProjectMigrationDeploymentRetired(ctx, build.DeploymentID)
				if err != nil || !retired {
					t.Fatal("old cleanup target remained runnable")
				}
			}
		})
	}
}
func drainRecoveryFixture(t *testing.T, f canaryStoreFixture, r api.ProjectSubmissionReceipt) {
	for _, build := range r.Apply.Builds {
		if _, err := f.store.UpdateDeploymentTraffic(t.Context(), build.DeploymentID, 100, ""); err != nil {
			t.Fatal(err)
		}
	}
	switch s := f.store.(type) {
	case *MemStore:
		s.mu.Lock()
		for id, graph := range s.projectReleaseSets {
			if graph.ProjectID == f.project && id != r.Apply.SafeRelease.ReleaseID {
				past := time.Now().Add(-time.Hour)
				graph.ExpiresAt = &past
				s.projectReleaseSets[id] = graph
			}
		}
		for id := range s.revisionPins {
			s.revisionPins[id] = time.Now().Add(-time.Hour)
		}
		s.mu.Unlock()
	case *PgStore:
		if _, err := s.pool.Exec(t.Context(), "UPDATE project_release_sets SET expires_at=clock_timestamp()-interval '1 hour' WHERE project_id=$1 AND id<>$2", f.project, r.Apply.SafeRelease.ReleaseID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(t.Context(), "UPDATE deployment_revision_pins SET expires_at=clock_timestamp()-interval '1 hour' WHERE app_id IN (SELECT id FROM apps WHERE project_id=$1)", f.project); err != nil {
			t.Fatal(err)
		}
	}
}
func editRecoveryReceipt(t *testing.T, store Store, account string, r api.ProjectSubmissionReceipt) {
	raw, _ := json.Marshal(r)
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		entry := s.projectSubmissions[account+"\x00"+r.AttemptID]
		entry.Payload = raw
		s.projectSubmissions[account+"\x00"+r.AttemptID] = entry
		s.mu.Unlock()
	case *PgStore:
		if _, err := s.pool.Exec(t.Context(), "UPDATE project_submissions SET payload=$1 WHERE account_id=$2 AND attempt_id=$3", raw, account, r.AttemptID); err != nil {
			t.Fatal(err)
		}
	}
}
func deleteRecoveryTaskFixture(t *testing.T, store Store, id string) {
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		delete(s.appTasks, id)
		s.mu.Unlock()
	case *PgStore:
		if _, err := s.pool.Exec(t.Context(), "DELETE FROM app_tasks WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
	}
}
