//go:build metal

// adr: 20261019120008 — qualify the real project release owner, queue gateway and guests.
package e2e_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Every child must execute on the native gate. There are no child-level skips,
// fake VM responses, injected readiness proofs or direct graph publications.
func TestProjectWorkerReleaseMetal(t *testing.T) {
	f := newProjectWorkerNativeFixture(t)
	if !t.Run("isolation_restart_cutover_rollback", func(t *testing.T) { f.qualifyHandoffAndRollback(t) }) {
		return
	}
	if !t.Run("startup_failure_preserves_production", func(t *testing.T) { f.qualifyStartupFailure(t) }) {
		return
	}
	t.Run("ignored_signal_bounded_drain_and_success", func(t *testing.T) { f.qualifyForcedDrain(t) })
}

func (f *projectWorkerNativeFixture) baselineWorker(t *testing.T) state.Instance {
	t.Helper()
	for _, member := range f.baseline.Members {
		if member.AppID == f.apps["processor"].ID {
			return f.worker(t, member.DeploymentID)
		}
	}
	t.Fatal("baseline graph lost its worker")
	return state.Instance{}
}

func (f *projectWorkerNativeFixture) readyCandidates(t *testing.T, accepted api.ApplyResponse) state.Instance {
	t.Helper()
	workerDeployment := ""
	for _, build := range accepted.Builds {
		ctx, cancel := context.WithTimeout(t.Context(), 16*time.Minute)
		_, _, err := e2etest.WaitForSourceDeployment(ctx, t, f.h.Pool, build.DeploymentID, e2etest.DefaultBuildStallWindow, 15*time.Minute)
		cancel()
		mustProjectWorkerNative(t, err)
		if build.AppID == f.apps["processor"].ID {
			workerDeployment = build.DeploymentID
		}
	}
	if workerDeployment == "" {
		t.Fatal("candidate receipt lost its worker")
	}
	f.assertGraph(t, f.baseline.ID)
	return f.worker(t, workerDeployment)
}

func (f *projectWorkerNativeFixture) qualifyHandoffAndRollback(t *testing.T) {
	old := f.baselineWorker(t)
	before := f.enqueue(t, "baseline", false)
	f.awaitCompleted(t, before)
	assertProjectWorkerReceived(t, old, "baseline", "stable", "stable-config")
	accepted, attempt := f.candidate(t, "candidate", false, true)
	candidate := f.readyCandidates(t, accepted)
	assertProjectWorkerIsolated(t, candidate, "candidate", "candidate-config")
	// Recreate schedd's ledger and hold lookup from durable PostgreSQL state
	// while the release owner remains down. Actual production queue work must
	// still reach the retained guest and the candidate must stay resident.
	mustProjectWorkerNative(t, f.h.RestartSchedd())
	preCutover := f.enqueue(t, "before-cutover", false)
	f.awaitCompleted(t, preCutover)
	f.assertGraph(t, f.baseline.ID)
	f.assertHTTP(t, "stable", "stable-config")
	assertProjectWorkerReceived(t, old, "before-cutover", "stable", "stable-config")
	assertProjectWorkerIsolated(t, candidate, "candidate", "candidate-config")
	if current := f.worker(t, candidate.DeploymentID); current.ID != candidate.ID {
		t.Fatal("schedd restart retired or recreated a held candidate")
	}
	// A real job stays inside the old guest until SIGTERM. Its successful ACK
	// after cutover proves graceful retirement rather than merely a stopped row.
	inFlight := f.enqueue(t, "in-flight-at-cutover", true)
	waitProjectWorkerNative(t, 10*time.Second, "held old-generation job", func() bool {
		return projectWorkerNativeState(t, old).Active == 1
	})
	mustProjectWorkerNative(t, f.h.RestartAPID())
	f.apidStopped = false
	verifying := f.receipt(t, attempt, "verifying")
	f.assertGraph(t, verifying.Apply.SafeRelease.ReleaseID)
	f.assertNoResubmission(t, accepted)
	f.awaitCompleted(t, inFlight)
	drain := f.awaitRetired(t, old)
	f.assertHTTP(t, "candidate", "candidate-config")
	afterCutover := f.enqueue(t, "after-cutover", false)
	f.awaitCompleted(t, afterCutover)
	assertProjectWorkerReceived(t, candidate, "after-cutover", "candidate", "candidate-config")
	// Regress only the configured critical probe; startup health stays healthy.
	// Real gateway samples, not synthetic evidence rows, must cause rollback.
	_, status := projectWorkerNativeRequest(t, f.h.EdgeURL()+"/control/regress", f.apps["frontend"].Slug+".apps.test.example", http.MethodPost)
	if status != http.StatusNoContent {
		t.Fatalf("candidate regression control status=%d", status)
	}
	rolledBack := f.receipt(t, attempt, "rolled_back")
	if rolledBack.Apply.SafeRelease.Worker == nil || rolledBack.Apply.SafeRelease.Worker.Status != "restored" {
		t.Fatal("rollback receipt lost restored worker admission")
	}
	var err error
	f.baseline, err = f.owner.GetActiveProjectReleaseSet(t.Context(), f.project.Slug, "production")
	mustProjectWorkerNative(t, err)
	f.assertGraph(t, rolledBack.Apply.SafeRelease.RestoredReleaseID)
	f.assertHTTP(t, "stable", "stable-config")
	restored := f.baselineWorker(t)
	afterRollback := f.enqueue(t, "after-rollback", false)
	f.awaitCompleted(t, afterRollback)
	assertProjectWorkerReceived(t, restored, "after-rollback", "stable", "stable-config")
	f.awaitRetired(t, candidate)
	for _, id := range f.completed {
		inv, err := f.store.InvocationByID(t.Context(), id)
		mustProjectWorkerNative(t, err)
		if inv.State != state.InvocationCompleted || inv.Attempts != 1 {
			t.Fatalf("rollback reversed or replayed completed job %s: %+v", id, inv)
		}
	}
	f.assertNoResubmission(t, accepted)
	t.Logf("ADR-20261019120008 native handoff: attempt=%s old_worker=%s candidate_worker=%s restored_worker=%s drain=%s; completed jobs preserved=%d", attempt, old.ID, candidate.ID, restored.ID, drain, len(f.completed))
}

func (f *projectWorkerNativeFixture) qualifyStartupFailure(t *testing.T) {
	old := f.baselineWorker(t)
	accepted, attempt := f.candidate(t, "broken", true, false)
	failed := f.receipt(t, attempt, "failed")
	if failed.Apply.SafeRelease.ReleaseID != "" {
		t.Fatal("failed worker startup published a candidate graph")
	}
	f.assertGraph(t, f.baseline.ID)
	f.assertHTTP(t, "stable", "stable-config")
	job := f.enqueue(t, "after-startup-failure", false)
	f.awaitCompleted(t, job)
	assertProjectWorkerReceived(t, old, "after-startup-failure", "stable", "stable-config")
	f.assertNoResubmission(t, accepted)
	t.Logf("ADR-20261019120008 native startup failure: attempt=%s graph=%s production_worker=%s job=%s", attempt, f.baseline.ID, old.ID, job)
}

func (f *projectWorkerNativeFixture) qualifyForcedDrain(t *testing.T) {
	old := f.baselineWorker(t)
	url := "http://" + net.JoinHostPort(old.HostIP, "8080") + "/control/ignore-stop"
	_, status := projectWorkerNativeRequest(t, url, "", http.MethodPost)
	if status != http.StatusNoContent {
		t.Fatalf("ignored-stop control status=%d", status)
	}
	if !projectWorkerNativeState(t, old).IgnoreStop {
		t.Fatal("native guest did not enter ignored-SIGTERM mode")
	}
	accepted, attempt := f.candidate(t, "successor", false, true)
	candidate := f.readyCandidates(t, accepted)
	assertProjectWorkerIsolated(t, candidate, "successor", "successor-config")
	mustProjectWorkerNative(t, f.h.RestartAPID())
	f.apidStopped = false
	verifying := f.receipt(t, attempt, "verifying")
	f.assertGraph(t, verifying.Apply.SafeRelease.ReleaseID)
	drain := f.awaitRetired(t, old)
	job := f.enqueue(t, "after-forced-drain", false)
	f.awaitCompleted(t, job)
	assertProjectWorkerReceived(t, candidate, "after-forced-drain", "successor", "successor-config")
	succeeded := f.receipt(t, attempt, "succeeded")
	if succeeded.Apply.SafeRelease.Verification.HealthyRounds < api.ProjectReleaseVerificationRounds {
		t.Fatal("successful release skipped native critical-probe rounds")
	}
	f.assertHTTP(t, "successor", "successor-config")
	f.assertNoResubmission(t, accepted)
	t.Logf("ADR-20261019120008 native forced drain: attempt=%s retired_worker=%s active_worker=%s drain=%s grace=%s graph=%s", attempt, old.ID, candidate.ID, drain, projectWorkerNativeGrace, succeeded.Apply.SafeRelease.ReleaseID)
}

func assertProjectWorkerIsolated(t *testing.T, instance state.Instance, generation, mode string) {
	t.Helper()
	report := projectWorkerNativeState(t, instance)
	if report.Generation != generation || report.Mode != mode || len(report.Received) != 0 || report.Active != 0 || report.Stopping {
		t.Fatalf("candidate ran production work or lost startup configuration: %+v", report)
	}
}

func assertProjectWorkerReceived(t *testing.T, instance state.Instance, label, generation, mode string) {
	t.Helper()
	report := projectWorkerNativeState(t, instance)
	if report.Generation != generation || report.Mode != mode || report.Received[label] != 1 || report.Completed[label] != 1 {
		t.Fatalf("job %s reached wrong generation/configuration or replayed: %+v", label, report)
	}
}

func (f *projectWorkerNativeFixture) awaitRetired(t *testing.T, instance state.Instance) time.Duration {
	t.Helper()
	var requested time.Time
	waitProjectWorkerNative(t, 30*time.Second, "durable worker drain claim", func() bool {
		return f.h.Pool.QueryRow(t.Context(), `SELECT requested_at FROM project_release_worker_drains WHERE instance_id=$1`, instance.ID).Scan(&requested) == nil
	})
	// Signal grace is distinct from confirmed VM/network teardown. The extra
	// 15s bounds transport and resource release; it cannot extend guest grace.
	waitProjectWorkerNative(t, projectWorkerNativeGrace+15*time.Second, "confirmed native worker retirement", func() bool {
		fresh, err := f.store.InstanceByID(t.Context(), instance.ID)
		mustProjectWorkerNative(t, err)
		if fresh.State != string(state.StateStopped) {
			return false
		}
		_, err = os.Lstat(filepath.Join("/run/netns", instance.Netns))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal("inspect retired worker namespace:", err)
		}
		return errors.Is(err, os.ErrNotExist)
	})
	elapsed := time.Since(requested)
	if elapsed > projectWorkerNativeGrace+15*time.Second {
		t.Fatalf("old worker exceeded grace plus bounded teardown: %s", elapsed)
	}
	return elapsed
}
