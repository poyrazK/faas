//go:build metal

package e2e_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (j *nativeSourceJobJourney) runSourceJob(t *testing.T) {
	firstSHA, heldSHA, nextSHA := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	var first, held, changed api.ApproveEnvironmentGitRevisionResponse
	var initial, current state.Deployment
	var firstGraph string
	seen := map[string]bool{}
	if !t.Run("qualified_schedule", func(t *testing.T) {
		first = j.approve(t, firstSHA)
		preview, err := j.store.PreviewEnvironmentGitOpsAdoption(t.Context(), j.source.AccountID, j.source.ID)
		if err != nil || !preview.CanApply() {
			t.Fatalf("source Job adoption preview: %+v %v", preview, err)
		}
		if err := j.store.AdoptEnvironmentGitOps(t.Context(), j.source.AccountID, j.source.ID, preview.Hash); err != nil {
			t.Fatal(err)
		}
		initial = j.waitSourceCandidate(t, first, true)
		job := j.sourceJob(t)
		if job.Status != "paused" || job.ImageMaterializationStatus != "pending" || job.ImageStorageKey != "" {
			t.Fatalf("unfinished source build opened its schedule: %+v", job)
		}
		runs, err := j.store.JobRunListByJob(t.Context(), job.ID, 20, 0)
		if err != nil || len(runs) != 0 {
			t.Fatalf("unqualified source Job admitted runs: %d %v", len(runs), err)
		}
		activated := j.waitSourceActivation(t, first)
		firstGraph = activated.WorkloadEvidence.GraphID
		initial = j.assertSourceArtifact(t, initial.ID, firstSHA, true)
		j.waitSourceOccurrence(t, initial, seen)
		j.waitSourceApplied(t, first.Revision.ID, first.Source.Generation)
		j.assertSourceBuildCount(t, 1)
	}) {
		return
	}
	if !t.Run("artifact_reuse", func(t *testing.T) {
		j.waitSourceOccurrence(t, initial, seen)
		j.assertSourceArtifact(t, initial.ID, firstSHA, false)
		j.assertSourceBuildCount(t, 1)
	}) {
		return
	}
	if !t.Run("scheduler_restart", func(t *testing.T) {
		before := j.sourceJob(t)
		if err := j.h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
		if err := j.h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		if err := j.h.RestartAPID(); err != nil {
			t.Fatal(err)
		}
		j.waitSourceOccurrence(t, initial, seen)
		after := j.sourceJob(t)
		if after.ID != before.ID || after.ScheduleRevision != before.ScheduleRevision || after.LastScheduledAt == nil ||
			before.LastScheduledAt == nil || after.LastScheduledAt.Before(*before.LastScheduledAt) {
			t.Fatal("scheduler restart replaced the Job or lost its occurrence cursor")
		}
		j.assertSourceArtifact(t, initial.ID, firstSHA, false)
		j.assertSourceBuildCount(t, 1)
	}) {
		return
	}
	if !t.Run("held_build_supersession", func(t *testing.T) {
		j.addSourceRevision(t, heldSHA, true)
		held = j.approve(t, heldSHA)
		candidate := j.waitSourceCandidate(t, held, true)
		if job := j.sourceJob(t); job.Status != "paused" || job.ImageStorageKey != "" {
			t.Fatalf("held build borrowed a serving artifact: %+v", job)
		}
		if err := j.h.RestartAPID(); err != nil {
			t.Fatal(err)
		}
		resumed := j.waitSourceCandidate(t, held, false)
		if resumed.ID != candidate.ID || resumed.BuildID != candidate.BuildID {
			t.Fatal("apid restart duplicated the in-flight source build")
		}
		j.addSourceRevision(t, nextSHA, false)
		changed = j.approve(t, nextSHA)
		if changed.Source.Generation <= held.Source.Generation {
			t.Fatal("source supersession did not advance generation")
		}
		current = j.waitSourceCandidate(t, changed, false)
		j.waitSourceActivation(t, changed)
		current = j.assertSourceArtifact(t, current.ID, nextSHA, true)
		j.waitSourceOccurrence(t, current, seen)
		j.waitSourceApplied(t, changed.Revision.ID, changed.Source.Generation)
		j.assertNoSupersededServing(t, held.Source.Generation)
		j.assertNoSourceRuns(t, heldSHA)
	}) {
		return
	}
	t.Run("reviewed_rollback", func(t *testing.T) {
		rollback := j.approve(t, firstSHA)
		if rollback.Revision.ID != first.Revision.ID || rollback.Source.Generation <= changed.Source.Generation {
			t.Fatal("rollback did not reapprove the immutable source revision at a new generation")
		}
		candidate := j.waitSourceCandidate(t, rollback, false)
		if candidate.ID == initial.ID || candidate.ID == current.ID || candidate.BuildID == initial.BuildID || candidate.BuildID == current.BuildID {
			t.Fatal("rollback reused an earlier candidate's build or qualification authority")
		}
		activated := j.waitSourceActivation(t, rollback)
		if activated.WorkloadEvidence.GraphID == firstGraph {
			t.Fatal("rollback reused the first graph")
		}
		candidate = j.assertSourceArtifact(t, candidate.ID, firstSHA, false)
		j.waitSourceOccurrence(t, candidate, seen)
		j.waitSourceApplied(t, rollback.Revision.ID, rollback.Source.Generation)
		j.assertNoSupersededServing(t, held.Source.Generation)
		j.assertNoSourceRuns(t, heldSHA)
	})
}

func (j *nativeSourceJobJourney) sourceJob(t *testing.T) state.Job {
	t.Helper()
	intent, err := j.store.EnvironmentWorkloadIntent(t.Context(), j.source.AccountID, j.app.ID, j.source.EnvironmentID)
	if err != nil || intent.JobID == "" || j.jobID != "" && j.jobID != intent.JobID {
		t.Fatalf("source Job lost its stable workload identity: %v", err)
	}
	j.jobID = intent.JobID
	job, err := j.store.JobGetByID(t.Context(), intent.JobID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (j *nativeSourceJobJourney) waitSourceCandidate(t *testing.T, approved api.ApproveEnvironmentGitRevisionResponse, running bool) state.Deployment {
	t.Helper()
	var candidate state.Deployment
	j.waitStatus(t, "source builder admission", 18*time.Minute, func(_ api.EnvironmentGitOpsStatusResponse) bool {
		deployments, err := j.store.ListDeploymentsForApp(t.Context(), j.app.ID, 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, dep := range deployments {
			frozen, err := dep.ScopedWorkloadRuntime()
			if err != nil || frozen == nil || frozen.SourceID != j.source.ID || frozen.Generation != approved.Source.Generation || frozen.RevisionID != approved.Revision.ID {
				continue
			}
			build, err := j.store.BuildByID(t.Context(), dep.BuildID)
			if err != nil {
				t.Fatal(err)
			}
			if dep.Status == state.DeployFailed || build.Status == state.BuildFailed || build.Status == state.BuildCancelled {
				t.Fatalf("native source build failed: deployment=%s status=%s build=%s failure=%s", dep.ID, dep.Status, build.Status, build.FailureClass)
			}
			if running && build.Status != state.BuildRunning {
				continue
			}
			if running && !dep.EnvironmentWorkloadHeld() {
				t.Fatal("running source build escaped qualification hold")
			}
			candidate = dep
			return true
		}
		return false
	})
	return candidate
}

func (j *nativeSourceJobJourney) waitSourceActivation(t *testing.T, approved api.ApproveEnvironmentGitRevisionResponse) api.EnvironmentGitOpsStatusResponse {
	t.Helper()
	return j.waitStatus(t, "source Job native qualification and activation", 20*time.Minute, func(s api.EnvironmentGitOpsStatusResponse) bool {
		e := s.WorkloadEvidence
		return s.Source.ApprovedRevisionID == approved.Revision.ID && s.Source.Generation == approved.Source.Generation &&
			e != nil && e.RevisionID == approved.Revision.ID && e.Generation == approved.Source.Generation &&
			e.Qualified && e.Activated && e.JobSmokesRecorded == 1 && e.GuestConfigAcknowledgementsRecorded == 1
	})
}

func (j *nativeSourceJobJourney) assertSourceArtifact(t *testing.T, deploymentID, sha string, nativeBuild bool) state.Deployment {
	t.Helper()
	job := j.sourceJob(t)
	dep, err := j.store.DeploymentByID(t.Context(), deploymentID)
	if err != nil || dep.Status != state.DeployLive || dep.EnvironmentWorkloadHeld() || dep.CommitSHA != sha {
		t.Fatalf("source Job selected an unqualified deployment: %v", err)
	}
	intent, err := j.store.EnvironmentWorkloadIntent(t.Context(), j.source.AccountID, j.app.ID, j.source.EnvironmentID)
	if err != nil || job.Status != "active" || !state.EnvironmentGitOpsSourceJobArtifactMatches(intent, job, dep) {
		t.Fatalf("source Job artifact binding mismatch: %v", err)
	}
	build, err := j.store.BuildByID(t.Context(), dep.BuildID)
	if err != nil || build.Status != state.BuildSucceeded || nativeBuild && build.CacheStatus == "hit" {
		t.Fatalf("native source build not proven: status=%s cache=%s err=%v", build.Status, build.CacheStatus, err)
	}
	proof, err := j.store.BuildProvenanceByBuildID(t.Context(), dep.BuildID)
	if err != nil || proof.SourceSHA256 != dep.SourceSHA256 || proof.CommitSHA != sha || proof.SourceURL != dep.SourceURL || proof.BuilderNodeID == "" || proof.FinishedAt.Before(proof.StartedAt) {
		t.Fatalf("native source build provenance mismatch: %v", err)
	}
	if nativeBuild {
		done := readBuildDone(t, j.h, dep.BuildID)
		tool, marker := "railpack", "railpack"
		if j.kind == "dockerfile" {
			// guest-init runs buildctl without a shell trace; BuildKit's
			// Dockerfile frontend reports the definition it executed.
			tool, marker = "buildctl", "load build definition from"
		}
		if done.ExitCode != 0 || !strings.Contains(strings.ToLower(done.LogTail), marker) {
			t.Fatalf("native builder did not prove %s execution", tool)
		}
	}
	return dep
}

// waitSourceApplied waits for the Job graph's serving receipt and, if it never
// arrives, records the fields its scheduled-run proof compares; the test
// database does not outlive the run.
func (j *nativeSourceJobJourney) waitSourceApplied(t *testing.T, revision string, generation int64) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		ctx := context.WithoutCancel(t.Context())
		intent, err := j.store.EnvironmentWorkloadIntent(ctx, j.source.AccountID, j.app.ID, j.source.EnvironmentID)
		if err != nil || intent.JobID == "" {
			t.Logf("serving proof: no Job binding: %v", err)
			return
		}
		job, err := j.store.JobGetByID(ctx, intent.JobID)
		if err != nil {
			t.Logf("serving proof: job: %v", err)
			return
		}
		t.Logf("serving proof job: status=%s kind=%s rev=%d image=%s/%s/%s materialized=%s retry=%d timeout=%d par=%d ram=%d cmd=%q env=%s rules=%+v",
			job.Status, job.Kind, job.ScheduleRevision, job.ImageRef, job.ImageResolvedDigest, job.ImageStorageKey, job.ImageMaterializationStatus,
			job.RetryMax, job.TaskTimeoutS, job.MaxParallelism, job.RAMMB, job.Command, job.EnvOverrides, job.FailureRules)
		t.Logf("serving proof schedule: job cron=%q tz=%q policy=%+v; intent schedule=%+v", job.CronSchedule, job.CronTimezone, job.SchedulePolicy, intent.Schedule)
		occurrences, err := j.store.ScheduleOccurrenceListByJob(ctx, job.ID, 5, "")
		t.Logf("serving proof occurrences (err=%v):", err)
		for _, o := range occurrences {
			t.Logf("  %+v", o)
		}
		runs, err := j.store.JobRunListByJob(ctx, job.ID, 5, 0)
		t.Logf("serving proof runs (err=%v):", err)
		for _, r := range runs {
			t.Logf("  id=%s occ=%s trigger=%s status=%s created=%s started=%v finished=%v retry=%v timeout=%v par=%d tasks=%d class=%s policy=%s ram=%v image=%s/%s/%s cmd=%q env=%s effective=%s rules=%+v input=%d/%q/%q/%q",
				r.ID, r.OccurrenceID, r.TriggerKind, r.AggregateStatus, r.CreatedAt, r.StartedAt, r.FinishedAt, r.RetryMax, r.TaskTimeoutS,
				r.Parallelism, r.Tasks, r.ExecutionClass, r.FailurePolicy, r.RAMMBSnapshot, r.ImageRefSnapshot, r.ImageResolvedDigestSnapshot,
				r.ImageStorageKeySnapshot, r.Command, r.EnvOverrides, r.EffectiveEnvSnapshot, r.FailureRules, r.InputManifestVersion,
				r.InputDigest, r.InputManifestURI, r.InputManifestSHA256)
		}
		j.logServingProofSQL(ctx, t, job.ID)
	})
	j.waitApplied(t, revision, generation)
}

// logServingProofSQL reads, without writing, each database check the Job
// graph's serving receipt depends on.
func (j *nativeSourceJobJourney) logServingProofSQL(ctx context.Context, t *testing.T, jobID string) {
	if j.pool == nil {
		return
	}
	status, err := j.client.GetEnvironmentGitOps(ctx, j.project.Slug, "production")
	if err != nil || status.WorkloadEvidence == nil || status.WorkloadEvidence.GraphID == "" {
		t.Logf("serving proof: no graph: %v", err)
		return
	}
	graphID := status.WorkloadEvidence.GraphID
	query := func(label, sql string, args ...any) {
		rows, err := j.pool.Query(ctx, sql, args...)
		if err != nil {
			t.Logf("serving proof %s: %v", label, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			values, err := rows.Values()
			t.Logf("serving proof %s: %v %v", label, values, err)
		}
	}
	query("graph", `SELECT phase,generation,intent_version,revision_id::text,members::text FROM environment_workload_graphs WHERE id=$1::uuid`, graphID)
	query("source", `SELECT mode,suspended,generation,intent_version,approved_revision_id::text FROM environment_git_sources WHERE id=$1::uuid`, j.source.ID)
	query("receipt", `SELECT phase,release_set_id::text,expected_gateways,started_at,updated_at FROM environment_workload_serving_receipts WHERE graph_id=$1::uuid`, graphID)
	query("releases", `SELECT id::text,active,environment_slug,created_at FROM project_release_sets WHERE project_id=$1::uuid ORDER BY created_at DESC LIMIT 3`, j.project.ID)
	query("readiness", `SELECT r.id::text,environment_workload_scheduled_jobs_ready($1::uuid,r.id),
		(SELECT count(*) FROM environment_workload_scheduled_job_acks($1::uuid,r.id)),
		environment_workload_serving_ready_without_scheduled_jobs($1::uuid),environment_workload_serving_gateway_roster_ready($1::uuid),
		environment_workload_serving_ready($1::uuid)
		FROM project_release_sets r WHERE r.project_id=$2::uuid AND r.active`, graphID, j.project.ID)
	query("artifact", `SELECT d.id::text,d.status,d.environment_workload_held,environment_workload_source_job_deployment_matches(j.id,d.id),
		j.image_materialization_status,j.image_storage_key=d.rootfs_key
		FROM jobs j JOIN deployments d ON j.image_resolved_digest='gregale-deployment:'||d.id::text WHERE j.id=$1::uuid`, jobID)
	query("gitops job", `SELECT desired_generation,claimed_generation,next_attempt_at,lease_until,attempt_count FROM environment_gitops_jobs WHERE source_id=$1::uuid`, j.source.ID)
	query("gitops runs", `SELECT status,error_code,started_at,completed_at,left(steps::text,800) FROM environment_gitops_runs WHERE source_id=$1::uuid ORDER BY started_at DESC LIMIT 4`, j.source.ID)
}

func (j *nativeSourceJobJourney) waitSourceOccurrence(t *testing.T, dep state.Deployment, seen map[string]bool) state.JobRun {
	t.Helper()
	var result state.JobRun
	j.waitStatus(t, "native scheduled Job completion", 4*time.Minute, func(_ api.EnvironmentGitOpsStatusResponse) bool {
		job := j.sourceJob(t)
		runs, err := j.store.JobRunListByJob(t.Context(), job.ID, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, run := range runs {
			if seen[run.ID] || run.ImageResolvedDigestSnapshot != "gregale-deployment:"+dep.ID {
				continue
			}
			if run.TriggerKind != "scheduled" || run.OccurrenceID == "" || run.ImageRefSnapshot != job.ImageRef || run.ImageStorageKeySnapshot != dep.RootfsKey {
				t.Fatal("scheduled occurrence did not freeze the qualified source artifact")
			}
			if run.AggregateStatus == "failed" || run.AggregateStatus == "cancelled" {
				t.Fatalf("native scheduled occurrence failed: run=%s status=%s", run.ID, run.AggregateStatus)
			}
			if run.AggregateStatus != "succeeded" {
				continue
			}
			tasks, err := j.store.JobTaskList(t.Context(), run.ID, 10, 0)
			if err != nil || len(tasks) != 1 || tasks[0].Status != "succeeded" || tasks[0].Attempt != 1 || tasks[0].ExitCode == nil || *tasks[0].ExitCode != 0 {
				t.Fatalf("native scheduled task evidence missing: %v", err)
			}
			logs, err := j.client.GetJobTaskLogs(t.Context(), job.Name, run.ID, 0)
			marker := "gitops-run:" + dep.CommitSHA + ":" + run.ID
			if err != nil || logs.TaskStatus != "succeeded" || logs.Truncated || strings.Count(logs.LogContent, marker) != 1 {
				t.Fatalf("guest output did not prove the exact built commit and run: %v", err)
			}
			seen[run.ID] = true
			result = run
			return true
		}
		return false
	})
	return result
}

func (j *nativeSourceJobJourney) assertSourceBuildCount(t *testing.T, expected int) {
	t.Helper()
	var count int
	if err := j.h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM builds b JOIN deployments d ON d.id=b.deployment_id WHERE d.app_id=$1`, j.app.ID).Scan(&count); err != nil || count != expected {
		t.Fatalf("scheduled occurrences rebuilt source: builds=%d want=%d err=%v", count, expected, err)
	}
}

func (j *nativeSourceJobJourney) assertNoSourceRuns(t *testing.T, sha string) {
	t.Helper()
	var count int
	if err := j.h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM job_runs WHERE job_id=$1 AND image_ref_snapshot=$2`,
		j.jobID, "gregale-source://"+j.source.EnvironmentID+"/"+j.app.ID+"/"+sha).Scan(&count); err != nil || count != 0 {
		t.Fatalf("superseded source build admitted an occurrence: count=%d err=%v", count, err)
	}
}
