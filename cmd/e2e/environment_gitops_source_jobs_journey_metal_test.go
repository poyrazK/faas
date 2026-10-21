//go:build metal

package e2e_test

import (
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
		j.waitApplied(t, first.Revision.ID, first.Source.Generation)
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
		j.waitApplied(t, changed.Revision.ID, changed.Source.Generation)
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
		j.waitApplied(t, rollback.Revision.ID, rollback.Source.Generation)
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
		tool := "railpack"
		if j.kind == "dockerfile" {
			tool = "buildctl"
		}
		if done.ExitCode != 0 || !strings.Contains(strings.ToLower(done.LogTail), tool) {
			t.Fatalf("native builder did not prove %s execution", tool)
		}
	}
	return dep
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
