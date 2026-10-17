//go:build promotionacceptance && metal

package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	probesql "github.com/onebox-faas/faas/pkg/e2etest/migrationprobe/sqlc"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentPromotionCoordinatedSuccessMetal(t *testing.T) {
	qualifyCoordinatedNative(t, "success")
}
func TestProjectEnvironmentPromotionCoordinatedRollbackMetal(t *testing.T) {
	qualifyCoordinatedNative(t, "rollback")
}
func TestProjectEnvironmentPromotionCoordinatedResolveMetal(t *testing.T) {
	qualifyCoordinatedNative(t, "resolve")
}

func coordinatedStatus(t *testing.T, p *environmentPromotionNative, id string, migrate bool) api.ProjectEnvironmentPromotionStatusResponse {
	t.Helper()
	s, err := p.owner.GetProjectEnvironmentPromotionStatus(t.Context(), p.project.Slug, "production", id)
	mustProjectWorkerNative(t, err)
	if s.Health == nil || s.Health.Policy.Worker == nil || (s.Health.Policy.Migration != nil) != migrate || len(s.Health.Policy.Probes) != 2 {
		t.Fatal("coordinated promotion lost immutable policy")
	}
	return s
}

func admitCoordinatedNative(t *testing.T, p *environmentPromotionNative, migrate bool) (string, api.PromoteProjectEnvironmentRequest, string, string) {
	t.Helper()
	for _, app := range p.apps {
		var count int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM deployments WHERE app_id=$1", app.ID).Scan(&count))
		p.counts[app.ID] = count
		var builds int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM builds b JOIN deployments d ON d.id=b.deployment_id WHERE d.app_id=$1", app.ID).Scan(&builds))
		p.buildCounts[app.ID] = builds
	}
	preview, err := p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), p.project.Slug, "production", "staging", true, true, migrate, true)
	mustProjectWorkerNative(t, err)
	if !preview.CanPromote || preview.Health == nil || preview.Health.Worker == nil || len(preview.Health.Probes) != 2 || (preview.Health.Migration != nil) != migrate {
		t.Fatalf("coordinated admission refused: %v", preview.BlockingReasons)
	}
	request := api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: preview.PromotionToken}
	key := uuid.NewString()
	accepted, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", request, key)
	mustProjectWorkerNative(t, err)
	if accepted.PromotionID == "" || accepted.Status != "running" {
		t.Fatal("coordinated admission was not durable")
	}
	return accepted.PromotionID, request, key, preview.DestinationConfigHash
}

func qualifyCoordinatedNative(t *testing.T, scenario string) {
	m := newPromotionMigrationNativeConfigured(t, "hold", true)
	p := m.p
	for name, app := range p.apps {
		if name == "processor" {
			continue
		}
		gate, err := p.owner.GetRouteHealthGate(t.Context(), app.Slug)
		mustProjectWorkerNative(t, err)
		_, err = p.owner.SetRouteHealthGate(t.Context(), app.Slug, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/critical", Probe: &api.RouteHealthProbe{Path: "/critical"}}}})
		mustProjectWorkerNative(t, err)
	}
	p.baselineSamples(t)
	retryID := ""
	if scenario != "resolve" {
		old := p.baselineWorker(t)
		p.controlWorker(t, old, "crash-next", http.StatusNoContent)
		retryID = p.enqueue(t, "coordinated-pinned-retry", false)
		waitProjectWorkerNative(t, time.Minute, "deferred baseline delivery", func() bool {
			inv, err := p.store.InvocationByID(t.Context(), retryID)
			mustProjectWorkerNative(t, err)
			return inv.State == state.InvocationPending && inv.Attempts == 1
		})
	}
	id, request, key, configHash := admitCoordinatedNative(t, p, true)
	var migrating api.ProjectEnvironmentPromotionStatusResponse
	waitProjectWorkerNative(t, time.Minute, "real coordinated schema expansion", func() bool {
		migrating = coordinatedStatus(t, p, id, true)
		rowID, _ := m.config.ID()
		row, err := probesql.New().ObservePromotion(t.Context(), m.target.Data, rowID)
		mustProjectWorkerNative(t, err)
		return row.Applied && migrating.Health.Migration.TaskID != ""
	})
	m.observe(t, true)
	p.assertGraph(t, p.baseline.ID)
	held, err := p.store.HeldProjectReleaseWorkers(t.Context(), p.apps["processor"].ID)
	mustProjectWorkerNative(t, err)
	for _, w := range migrating.Workloads {
		if w.WorkloadName != "processor" {
			continue
		}
		for _, dep := range held {
			if dep == w.TargetDeploymentID {
				t.Fatal("worker held while SQL outcome unconfirmed")
			}
		}
		instances, err := p.store.ListInstancesForApp(t.Context(), p.apps["processor"].ID)
		mustProjectWorkerNative(t, err)
		for _, instance := range instances {
			if instance.DeploymentID == w.TargetDeploymentID {
				t.Fatal("candidate worker started before SQL receipt")
			}
		}
	}
	p.job(t, "migration-barrier-baseline", "baseline", "baseline-config")
	taskID := migrating.Health.Migration.TaskID
	task, err := p.owner.GetAppTask(t.Context(), p.apps["backend"].Slug, taskID)
	mustProjectWorkerNative(t, err)
	if task.Status != api.AppTaskStatusRunning || task.ExitCode != nil {
		t.Fatal("guest result barrier did not retain running task")
	}
	mustProjectWorkerNative(t, p.h.RestartAPID())
	restarted := coordinatedStatus(t, p, id, true)
	if restarted.Health.Migration.TaskID != taskID || !restarted.Health.Deadline.Equal(migrating.Health.Deadline) {
		t.Fatal("migration restart reset task or deadline")
	}
	evidence := map[string]any{"schema_version": 1, "scenario": "coordinated_" + scenario, "promotion_id": id, "task_id": taskID, "run_id": m.config.RunID, "baseline_graph": p.baseline.ID, "migration_owner": p.apps["backend"].ID, "worker_app": p.apps["processor"].ID, "worker_dormant": true, "migration_restart": true, "destination_only": true}
	if scenario == "resolve" {
		evidence["recovery"] = m.resolveCoordinatedNative(t, id, taskID)
	} else {
		rowID, _ := m.config.ID()
		mustProjectWorkerNative(t, probesql.New().ReleasePromotion(t.Context(), m.target.Data, rowID))
		final, cutover := finishCoordinatedNative(t, p, id, configHash, true, scenario == "rollback", evidence)
		p.assertArtifacts(t, final)
		beforeRelease, beforeDeployment := p.pin(t, retryID)
		if beforeRelease != p.baseline.ID || beforeDeployment == final.Health.Worker.DeploymentID {
			t.Fatal("retained retry lost baseline pin")
		}
		waitProjectWorkerNative(t, 9*time.Minute, "cross-cutover retry on retained worker", func() bool {
			inv, err := p.store.InvocationByID(t.Context(), retryID)
			mustProjectWorkerNative(t, err)
			if inv.State == state.InvocationDeadLetter || inv.State == state.InvocationFailed {
				t.Fatal("retained retry failed")
			}
			return inv.State == state.InvocationCompleted
		})
		retry, err := p.store.InvocationByID(t.Context(), retryID)
		mustProjectWorkerNative(t, err)
		var report projectWorkerNativeReport
		mustProjectWorkerNative(t, json.Unmarshal(retry.Result, &report))
		afterRelease, afterDeployment := p.pin(t, retryID)
		if retry.Attempts != 2 || retry.CompletedAt == nil || !retry.CompletedAt.After(cutover.Health.StartedAt) || afterRelease != beforeRelease || afterDeployment != beforeDeployment || report.Generation != "baseline" || report.Mode != "baseline-config" || !report.DestinationSecretMatches {
			t.Fatal("retry did not retain original graph/configuration across cutover")
		}
		evidence["retry_attempts"], evidence["retry_release"], evidence["retry_deployment"] = retry.Attempts, afterRelease, afterDeployment
	}
	before, err := p.owner.GetAppTask(t.Context(), p.apps["backend"].Slug, taskID)
	mustProjectWorkerNative(t, err)
	if before.AttemptCount != 1 || before.RetryMax != 0 || before.FinishedAt == nil || scenario == "resolve" && before.ExitCode != nil || scenario != "resolve" && (before.ExitCode == nil || *before.ExitCode != 0) {
		t.Fatal("SQL task lost its single original outcome")
	}
	if scenario != "resolve" && !strings.Contains(before.StdoutTail, "native promotion expansion generation=tested mode=tested-config") {
		t.Fatal("SQL did not execute the tested artifact/configuration")
	}
	mustProjectWorkerNative(t, p.h.RestartAPID())
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	replay, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "production", request, key)
	mustProjectWorkerNative(t, err)
	after, err := p.owner.GetAppTask(t.Context(), p.apps["backend"].Slug, taskID)
	mustProjectWorkerNative(t, err)
	if replay.PromotionID != id || !reflect.DeepEqual(before, after) {
		t.Fatal("terminal restart or replay rewrote SQL task")
	}
	row := m.observe(t, true)
	var tasks int
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM app_tasks WHERE app_id=$1 AND kind='release' AND deployment_scope='production'", p.apps["backend"].ID).Scan(&tasks))
	if tasks != 1 {
		t.Fatal("coordinated operation replayed SQL")
	}
	evidence["executions"], evidence["task_count"], evidence["attempts"], evidence["exit_code"] = row.Executions, tasks, before.AttemptCount, before.ExitCode
	evidence["schema_preserved"], evidence["terminal_restart"] = true, true
	p.cleanup(t)
	if !t.Failed() {
		evidence["cleanup"] = true
		logPromotionNativeEvidence(t, evidence)
	}
}

func finishCoordinatedNative(t *testing.T, p *environmentPromotionNative, id, configHash string, migrate, rollback bool, evidence map[string]any) (api.ProjectEnvironmentPromotionStatusResponse, api.ProjectEnvironmentPromotionStatusResponse) {
	t.Helper()
	var preflight, cutover, final api.ProjectEnvironmentPromotionStatusResponse
	waitProjectWorkerNative(t, time.Minute, "coordinated preflight startup", func() bool {
		preflight = coordinatedStatus(t, p, id, migrate)
		return preflight.Health.Phase == "preflight" && preflight.Health.Worker.InstanceID != ""
	})
	p.assertGraph(t, p.baseline.ID)
	frontend := p.graphCandidate(t, preflight, "frontend")
	backend := p.graphCandidate(t, preflight, "backend")
	mustProjectWorkerNative(t, p.h.KillAPID())
	p.apidStopped = true
	candidate := p.worker(t, preflight.Health.Worker.DeploymentID)
	assertProjectWorkerIsolated(t, candidate, "tested", "tested-config")
	if !projectWorkerNativeState(t, candidate).DestinationSecretMatches {
		t.Fatal("worker lost destination secret")
	}
	p.killWorkerVM(t, candidate)
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	candidate = p.replacement(t, candidate)
	private := p.graphReport(t, frontend, backend.DeploymentID, id, "tested", "https")
	checkpoint, err := p.store.ProjectEnvironmentPromotionHealth(t.Context(), p.accountID, id)
	mustProjectWorkerNative(t, err)
	if checkpoint.Phase != "preflight" || !checkpoint.Deadline.Equal(preflight.Health.Deadline) {
		t.Fatal("worker replacement reset preflight")
	}
	mustProjectWorkerNative(t, p.h.RestartAPID())
	p.apidStopped = false
	rounds := 0
	waitProjectWorkerNative(t, 5*time.Minute, "atomic coordinated cutover", func() bool {
		s := coordinatedStatus(t, p, id, migrate)
		if s.Health.Phase == "preflight" || s.Health.Phase == "cutover" {
			rounds = max(rounds, s.Health.HealthyRounds)
			p.assertGraph(t, p.baseline.ID)
			return false
		}
		if s.Health.Phase != "verifying" || s.ReleaseGraph == nil {
			t.Fatal("coordinated promotion ended before cutover", s.Health.Phase)
		}
		cutover = s
		return true
	})
	if rounds < api.ProjectReleaseVerificationRounds {
		t.Fatal("missing real coordinated preflight rounds")
	}
	p.assertGraph(t, cutover.ReleaseGraph.TargetReleaseSetID)
	p.assertConfig(t, configHash)
	p.assertHTTP(t, "tested", "tested-config")
	p.graphReport(t, frontend, backend.DeploymentID, cutover.ReleaseGraph.TargetReleaseSetID, "tested", "https")
	mustProjectWorkerNative(t, p.h.RestartAPID())
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	resumed := coordinatedStatus(t, p, id, migrate)
	if !resumed.Health.Deadline.Equal(cutover.Health.Deadline) || resumed.Health.Phase != "verifying" {
		t.Fatal("restart reset coordinated verification")
	}
	jobs, regressions := 0, 0
	var windows []time.Time
	workerRegression := false
	waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+time.Minute, "coordinated delivery outcome", func() bool {
		final = coordinatedStatus(t, p, id, migrate)
		h := final.Health
		regressions = max(regressions, h.RegressedRounds)
		if h.WorkerHealth != nil && h.WorkerHealth.Status == "regressed" && h.WorkerHealth.Candidate.Failures >= api.ProjectWorkerHealthMinFailures {
			workerRegression = true
		}
		if h.CheckedAt != nil && !h.CheckedAt.Before(cutover.Health.StartedAt) && h.Verdict == "healthy" && h.WorkerHealth != nil && h.WorkerHealth.Status == "healthy" && (len(windows) == 0 || h.CheckedAt.After(windows[len(windows)-1])) {
			windows = append(windows, *h.CheckedAt)
		}
		if h.Phase == "succeeded" && !rollback || h.Phase == "failed" && rollback && final.RollbackStatus == "rolled_back" {
			return true
		}
		if h.Phase != "verifying" && h.Phase != "finalizing" && h.Phase != "rolling_back" {
			t.Fatal("unexpected coordinated outcome", h.Phase)
		}
		if h.Phase == "verifying" {
			var jobID string
			if rollback {
				p.controlWorker(t, p.worker(t, candidate.DeploymentID), "fail-next", http.StatusNoContent)
				jobID = p.enqueue(t, fmt.Sprintf("regression-%d", jobs), false)
				waitProjectWorkerNative(t, time.Minute, "real worker failure", func() bool {
					inv, err := p.store.InvocationByID(t.Context(), jobID)
					mustProjectWorkerNative(t, err)
					return inv.State == state.InvocationPending && inv.Attempts == 1
				})
			} else {
				jobID = p.job(t, fmt.Sprintf("coordinated-%d", jobs), "tested", "tested-config").ID
			}
			graph, dep := p.pin(t, jobID)
			if graph != cutover.ReleaseGraph.TargetReleaseSetID || dep != candidate.DeploymentID {
				t.Fatal("job admission did not switch atomically")
			}
			jobs++
		}
		time.Sleep(time.Second)
		return false
	})
	if jobs < api.ProjectWorkerHealthMinAttempts || rollback && (!workerRegression || regressions < api.ProjectReleaseVerificationRounds) || !rollback && (len(windows) < 2 || windows[1].Sub(windows[0]) < time.Minute) {
		t.Fatal("missing real coordinated delivery windows")
	}
	selected, err := p.owner.GetActiveProjectReleaseSet(t.Context(), p.project.Slug, "production")
	mustProjectWorkerNative(t, err)
	if rollback {
		if final.ReleaseGraph == nil || selected.ID != final.ReleaseGraph.RestoredTargetReleaseSetID {
			t.Fatal("worker failure did not restore full graph")
		}
		p.assertConfig(t, p.targetConfig.ConfigHash)
		p.assertHTTP(t, "baseline", "baseline-config")
		p.job(t, "coordinated-restored", "baseline", "baseline-config")
	} else {
		p.assertGraph(t, cutover.ReleaseGraph.TargetReleaseSetID)
		p.assertConfig(t, configHash)
	}
	evidence["preflight_rounds"], evidence["healthy_windows"], evidence["regressed_rounds"], evidence["candidate_jobs"] = rounds, windows, regressions, jobs
	evidence["health_started_at"], evidence["cutover_graph"], evidence["selected_graph"], evidence["phase"] = cutover.Health.StartedAt, cutover.ReleaseGraph.TargetReleaseSetID, selected.ID, final.Health.Phase
	evidence["private_report"], evidence["worker_deployment"], evidence["worker_instance"] = private, candidate.DeploymentID, candidate.ID
	evidence["restart_preflight"], evidence["restart_verifying"], evidence["candidate_recovered"], evidence["destination_secrets"], evidence["worker_regression"] = true, true, true, true, workerRegression
	evidence["graph_config_restored"], evidence["artifact_reused"] = rollback, true
	evidence["destination_config_hash"] = configHash
	evidence["baseline_config_hash"] = p.targetConfig.ConfigHash
	evidence["selected_config_hash"] = configHash
	if rollback {
		evidence["selected_config_hash"] = p.targetConfig.ConfigHash
	}
	return final, cutover
}

func (p *environmentPromotionNative) graphCandidate(t *testing.T, s api.ProjectEnvironmentPromotionStatusResponse, name string) state.Instance {
	t.Helper()
	for _, w := range s.Workloads {
		if w.WorkloadName == name {
			return p.graphInstance(t, p.apps[name].ID, w.TargetDeploymentID)
		}
	}
	t.Fatal("missing candidate graph member", name)
	return state.Instance{}
}

func (m *promotionMigrationNative) repairCoordinatedSource(t *testing.T) {
	t.Helper()
	p := m.p
	source := projectWorkerNativeArchive(t, promotionCoordinatedNativeFiles(p.binary, "repair"))
	plan, err := p.owner.ScanProjectWithBindingEnvironment(t.Context(), bytes.NewReader(source), "project.tgz", p.project.Slug, "", "", 0, nil, nil, false, true, "staging")
	mustProjectWorkerNative(t, err)
	accepted, err := p.owner.ApplyProjectPlanWithSecrets(t.Context(), plan.PlanToken, bytes.NewReader(source), "project.tgz", p.project.Slug, "", "", 0, nil, nil, false, true, "staging", "", nil)
	mustProjectWorkerNative(t, err)
	if len(accepted.Builds) != 3 {
		t.Fatal("repair must replace all tested artifacts")
	}
	deployments := map[string]string{}
	for _, build := range accepted.Builds {
		ctx, cancel := context.WithTimeout(t.Context(), 16*time.Minute)
		_, _, err = e2etest.WaitForSourceDeployment(ctx, t, p.h.Pool, build.DeploymentID, e2etest.DefaultBuildStallWindow, 15*time.Minute)
		cancel()
		mustProjectWorkerNative(t, err)
		dep, err := p.store.DeploymentByID(t.Context(), build.DeploymentID)
		mustProjectWorkerNative(t, err)
		if len(dep.ReleaseCommand) != 0 {
			t.Fatal("repair retained a migration command")
		}
		p.source[build.AppID] = dep
		deployments[build.Slug] = dep.ID
	}
	_, err = p.owner.PublishProjectReleaseSet(t.Context(), p.project.Slug, "staging", api.PublishProjectReleaseSetRequest{TTLSeconds: 3600, Deployments: deployments})
	mustProjectWorkerNative(t, err)
}

func (m *promotionMigrationNative) resolveCoordinatedNative(t *testing.T, id, taskID string) map[string]any {
	t.Helper()
	p := m.p
	task, err := p.store.AppTaskByID(t.Context(), p.accountID, p.apps["backend"].ID, taskID)
	mustProjectWorkerNative(t, err)
	if task.LeaseExpiresAt == nil || task.LeaseToken == nil {
		t.Fatal("migration has no owned runtime lease")
	}
	leaseUntil := *task.LeaseExpiresAt
	mustProjectWorkerNative(t, p.h.KillSchedd())
	waitProjectWorkerNative(t, time.Minute, "original SQL owner lease expiry", func() bool { return !time.Now().Before(leaseUntil) })
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	var failed api.ProjectEnvironmentPromotionStatusResponse
	waitProjectWorkerNative(t, 3*time.Minute, "uncertain coordinated SQL result", func() bool {
		failed = coordinatedStatus(t, p, id, true)
		return failed.Status == "failed" && failed.Health.Migration.Status == "uncertain"
	})
	m.observe(t, true)
	p.assertGraph(t, p.baseline.ID)
	m.repairCoordinatedSource(t)
	p.baselineSamples(t)
	repairID, _, _, configHash := admitCoordinatedNative(t, p, false)
	repairEvidence := map[string]any{}
	repair, _ := finishCoordinatedNative(t, p, repairID, configHash, false, false, repairEvidence)
	p.assertArtifacts(t, repair)
	for i := 0; i < api.ProjectWorkerHealthMinAttempts; i++ {
		p.job(t, fmt.Sprintf("before-resolve-%d", i), "tested", "tested-config")
	}
	// Recovery remains available when new admission is disabled.
	for _, name := range []string{"FAAS_PROMOTION_MIGRATIONS_ENABLED", "FAAS_PROMOTION_WORKERS_ENABLED", "FAAS_PROMOTION_SERVICE_GRAPH_ENABLED", "FAAS_PROMOTION_HEALTH_ENABLED"} {
		mustProjectWorkerNative(t, p.h.SetAPIDEnv(name, "0"))
	}
	mustProjectWorkerNative(t, p.h.RestartAPID())
	mustProjectWorkerNative(t, p.h.KillSchedd())
	inspection := fmt.Sprintf("%s/%s/%s/executions=1/schema=expanded", id, taskID, repairID)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(inspection)))
	req := api.ProjectPromotionMigrationRecoveryRequest{RequestID: strings.ReplaceAll(uuid.NewString(), "-", ""), ExpectedTaskID: taskID, ExpectedGeneration: failed.Health.Generation, Action: "resolve", ReplacementPromotionID: repairID, DatabaseStateChecked: true, EvidenceHash: hash}
	receipt, err := p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, req)
	mustProjectWorkerNative(t, err)
	if receipt.Status != "quiescing" {
		t.Fatal("resolve bypassed executor teardown")
	}
	mustProjectWorkerNative(t, p.h.RestartAPID())
	replay, err := p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, req)
	mustProjectWorkerNative(t, err)
	if !reflect.DeepEqual(receipt, replay) {
		t.Fatal("quiescing restart changed decision")
	}
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	for i := 0; i < api.ProjectWorkerHealthMinAttempts; i++ {
		p.job(t, fmt.Sprintf("after-resolve-%d", i), "tested", "tested-config")
	}
	waitProjectWorkerNative(t, 3*time.Minute, "owned worker replacement resolution", func() bool {
		s := coordinatedStatus(t, p, id, true)
		return s.Health.Recovery != nil && s.Health.Recovery.Status == "resolved"
	})
	replay, err = p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, req)
	mustProjectWorkerNative(t, err)
	var destroyed *time.Time
	var runtime, lease string
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT runtime_instance_id::text,runtime_lease_token::text,destroyed_at FROM project_environment_promotion_migrations WHERE promotion_id=$1", id).Scan(&runtime, &lease, &destroyed))
	if destroyed == nil || destroyed.Before(receipt.RequestedAt) || lease != *task.LeaseToken || replay.ResolvedAt == nil || replay.ResolvedAt.Before(*destroyed) {
		t.Fatal("resolve lacks attributed teardown")
	}
	alive, err := probesql.New().PromotionSessionAlive(t.Context(), m.target.Data, m.config.PromotionSessionName())
	mustProjectWorkerNative(t, err)
	if alive {
		t.Fatal("original SQL executor remains connected")
	}
	var raw []byte
	mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT payload->'worker_proof' FROM project_environment_promotion_migration_recoveries WHERE promotion_id=$1 AND request_id=$2", id, req.RequestID).Scan(&raw))
	var proof struct {
		Worker      api.ProjectReleaseWorker `json:"worker"`
		Health      api.ProjectWorkerHealth  `json:"health"`
		WindowStart time.Time                `json:"window_start"`
		CheckedAt   time.Time                `json:"checked_at"`
	}
	mustProjectWorkerNative(t, json.Unmarshal(raw, &proof))
	if proof.Worker.DeploymentID != repair.Health.Worker.DeploymentID || proof.Worker.PreviousDeploymentID != proof.Worker.DeploymentID || proof.Worker.InstanceID == "" || proof.Health.Status != "healthy" || proof.Health.Baseline.Attempts < api.ProjectWorkerHealthMinAttempts || proof.Health.Candidate.Attempts < api.ProjectWorkerHealthMinAttempts || !proof.WindowStart.Equal(receipt.RequestedAt) || !proof.CheckedAt.After(proof.WindowStart) {
		t.Fatal("resolved receipt lacks fresh owned replacement deliveries")
	}
	final := coordinatedStatus(t, p, id, true)
	if final.Health.Migration.Status != "uncertain" || final.Status != "failed" {
		t.Fatal("resolution rewrote original SQL outcome")
	}
	p.assertGraph(t, repair.ReleaseGraph.TargetReleaseSetID)
	mustProjectWorkerNative(t, p.h.RestartAPID())
	mustProjectWorkerNative(t, p.h.RestartSchedd())
	again, err := p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, req)
	mustProjectWorkerNative(t, err)
	if !reflect.DeepEqual(replay, again) {
		t.Fatal("terminal recovery replay changed receipt")
	}
	changed := req
	changed.EvidenceHash = strings.Repeat("d", 64)
	if _, err = p.owner.RecoverProjectEnvironmentPromotionMigration(t.Context(), p.project.Slug, "production", id, changed); err == nil {
		t.Fatal("changed operator attestation reused request")
	}
	return map[string]any{"repair_promotion": repairID, "repair": repairEvidence, "runtime_id": runtime, "requested_at": receipt.RequestedAt, "destroyed_at": destroyed, "resolved_at": replay.ResolvedAt, "status": replay.Status, "worker_proof": json.RawMessage(raw), "session_gone": true, "receipt_preserved": true, "admission_disabled": true, "task_id": taskID}
}
