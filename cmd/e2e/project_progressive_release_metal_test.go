//go:build metal

package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Separate top-level tests make a missing or skipped rollback a lane failure.
func TestProjectProgressivePromotionMetal(t *testing.T) {
	qualifyProjectProgressive(t, false)
}

func TestProjectProgressiveRollbackMetal(t *testing.T) {
	qualifyProjectProgressive(t, true)
}

func TestProjectProgressiveStabilizationRollbackMetal(t *testing.T) {
	qualifyProjectProgressive(t, true, true)
}

func qualifyProjectProgressive(t *testing.T, rollback bool, options ...bool) {
	qualifyProjectProgressiveScenario(t, rollback, len(options) > 0 && options[0], "")
}

func qualifyProjectProgressiveScenario(t *testing.T, rollback, stabilize bool, contractMode string, setups ...projectContractSQLSetup) {
	qualifyProjectProgressiveMode(t, rollback, stabilize, false, contractMode, setups...)
}

func qualifyProjectProgressiveMode(t *testing.T, rollback, stabilize, httpOnly bool, contractMode string, setups ...projectContractSQLSetup) {
	qualifyProjectProgressiveFailureMode(t, rollback, stabilize, httpOnly, "", contractMode, setups...)
}

func qualifyProjectProgressiveFailureMode(t *testing.T, rollback, stabilize, httpOnly bool, failureMode, contractMode string, setups ...projectContractSQLSetup) {
	secretRelease := strings.HasPrefix(failureMode, "secrets_")
	if secretRelease {
		rollback = failureMode == "secrets_rollback"
		failureMode = ""
	}
	serviceGraph := strings.HasPrefix(failureMode, "service_")
	if serviceGraph {
		switch failureMode {
		case "service_promotion":
			failureMode = ""
		case "service_rollback":
			failureMode, rollback = "", true
		case "service_abort":
			failureMode = "early_abort"
		default:
			t.Fatal("unknown service graph scenario")
		}
	}

	env := []string{"FAAS_PROJECT_CANARIES_ENABLED=1"}
	var rotation *nativeReleaseSecretRotation
	if secretRelease {
		env = append(env, "FAAS_PROJECT_RELEASE_SECRETS_ENABLED=1")
		var keyEnv []string
		rotation, keyEnv = newNativeReleaseSecretRotation(t)
		env = append(env, keyEnv...)
	}
	if contractMode != "" {
		env = append(env, "FAAS_PROJECT_MIGRATION_CONTRACTS_ENABLED=1")
	}
	pinTTL := 3600
	if failureMode == "retention_expired" {
		pinTTL = int(api.ProjectReleaseVerificationMinRetention / time.Second)
	}
	if contractMode != "" {
		pinTTL = 1800
	}
	var setup func(*projectWorkerNativeFixture)
	if len(setups) > 0 {
		setup = setups[0].BeforeDeploy
		env = append(env, setups[0].Environment...)
	}
	if secretRelease {
		setup = func(f *projectWorkerNativeFixture) {
			f.secretRelease = true
			mustProjectWorkerNative(t, f.owner.SetSecretWithScope(t.Context(), f.apps["frontend"].Slug, "TOKEN", "stable-secret", "production"))
			mustProjectWorkerNative(t, f.store.UpsertAppEnvInScope(t.Context(), f.accountID, f.apps["frontend"].ID, "production", "RELEASE_PROOF", "sealed"))
		}
	}
	if failureMode == "retention_cold" {
		setup = func(f *projectWorkerNativeFixture) {
			timeout := api.IdleTimeoutFloorSeconds
			_, err := f.owner.UpdateApp(t.Context(), f.apps["frontend"].Slug, api.UpdateAppRequest{IdleTimeoutS: &timeout})
			mustProjectWorkerNative(t, err)
		}
	}
	f := newProjectReleaseNativeFixture(t, pinTTL, setup, httpOnly, serviceGraph, env...)
	var err error
	if !httpOnly {
		f.baselineWorker(t)
		_, err = f.owner.UpdateQueueBinding(t.Context(), f.apps["processor"].Slug, f.bindingID, api.UpdateQueueBindingRequest{RetryPolicy: &api.RetryPolicyDTO{MaxAttempts: 3, BaseSeconds: 1, MaxSeconds: 5}})
		mustProjectWorkerNative(t, err)
	}
	config := map[string]map[string]string{"frontend": {"MODE": "progressive-config"}}
	if serviceGraph {
		config["backend"] = map[string]string{"MODE": "progressive-config"}
		config["frontend"] = api.ServiceBindingEnvForTransport(config["frontend"], f.apps["frontend"].Manifest.ServiceBindings, f.apps["frontend"].Manifest.ServiceBindingTransport)
	}
	if !httpOnly {
		config["processor"] = map[string]string{"MODE": "progressive-config"}
	}
	releaseConfig := api.ProjectReleaseConfigRequest{Env: config}
	if secretRelease {
		stageID := strings.ReplaceAll(uuid.NewString(), "-", "")
		request := api.ProjectReleaseSecretStageRequest{RequestID: stageID, Workloads: map[string]map[string]string{"frontend": {"TOKEN": "progressive-secret"}}}
		versions, err := f.owner.StageProjectReleaseSecrets(t.Context(), f.project.Slug, request)
		mustProjectWorkerNative(t, err)
		replay, err := f.owner.StageProjectReleaseSecrets(t.Context(), f.project.Slug, request)
		mustProjectWorkerNative(t, err)
		if versions.Versions["frontend"]["TOKEN"] != replay.Versions["frontend"]["TOKEN"] {
			t.Fatal("secret staging replay changed version")
		}
		releaseConfig.Secrets = versions.Versions
		config["frontend"]["RELEASE_PROOF"] = "sealed"
	}
	raw, err := json.Marshal(releaseConfig)
	mustProjectWorkerNative(t, err)
	ctx := api.ContextWithProjectProgressiveRelease(api.ContextWithProjectWorkerHealth(
		api.ContextWithProjectReleaseWorkers(api.ContextWithProjectReleaseConfig(t.Context(), raw))))
	if httpOnly {
		ctx = api.ContextWithProjectProgressiveRelease(api.ContextWithProjectReleaseConfig(t.Context(), raw))
	}
	if stabilize {
		ctx = api.ContextWithProjectStabilization(ctx)
	}
	files := projectWorkerNativeFiles(f.binary, "progressive", false)
	if httpOnly {
		files = projectHTTPNativeFiles(f.binary, "progressive")
		if serviceGraph {
			files = projectServiceGraphNativeFiles(f.binary, "progressive")
		}
	}
	if contractMode != "" {
		ctx = api.ContextWithProjectReleaseMigration(ctx)
		files = projectContractNativeFiles(f.binary, contractMode)
	}
	source := projectWorkerNativeArchive(t, files)
	plan, err := f.scan(t, ctx, source)
	mustProjectWorkerNative(t, err)
	if plan.SafeRelease == nil || plan.SafeRelease.Verification == nil || !plan.SafeRelease.Verification.Progressive {
		t.Fatal("progressive policy missing from native scan")
	}
	attempt := strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx = api.ContextWithDeploymentAttempt(api.ContextWithIdempotencyKey(ctx, attempt), attempt)
	accepted, err := f.apply(t, ctx, plan.PlanToken, source)
	mustProjectWorkerNative(t, err)
	for _, build := range accepted.Builds {
		f.deploymentCounts[build.AppID]++
	}
	// Keep the owner down during real builds to prove durable acceptance recovery.
	mustProjectWorkerNative(t, f.h.KillAPID())
	f.apidStopped = true
	var candidate state.Instance
	if httpOnly {
		for _, build := range accepted.Builds {
			ctx, cancel := context.WithTimeout(t.Context(), 16*time.Minute)
			_, _, err := e2etest.WaitForSourceDeployment(ctx, t, f.h.Pool, build.DeploymentID, e2etest.DefaultBuildStallWindow, 15*time.Minute)
			cancel()
			mustProjectWorkerNative(t, err)
		}
	} else {
		candidate = f.readyCandidates(t, accepted)
		assertProjectWorkerIsolated(t, candidate, "progressive", "progressive-config")
	}
	// Historical baseline evidence comes from actual completed deliveries.
	for i := 0; !httpOnly && failureMode != "low_traffic" && i < 10; i++ {
		f.awaitCompleted(t, f.enqueue(t, fmt.Sprintf("baseline-%d", i), false))
	}
	mustProjectWorkerNative(t, f.h.RestartAPID())
	f.apidStopped = false
	f.receipt(t, attempt, "verifying")
	seen := map[int]bool{}
	httpSeen := map[string]bool{}
	restarted, regressed := false, false
	controlSent := false
	recoverySent := false
	probeUnavailable := false
	var controlReq api.ProjectReleaseControlRequest
	crashArmed := false
	crashedJob := ""
	crashedRelease, crashedDeployment := "", ""
	budget := 12 * time.Minute
	if failureMode == "full_recover" {
		budget += api.ProjectReleaseVerificationTimeout
	}
	if contractMode != "" {
		budget += api.ProjectReleaseStabilizationWindow
	}
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		policy, err := f.store.ProjectCanaryCheckpoint(t.Context(), f.accountID, attempt)
		mustProjectWorkerNative(t, err)
		seen[policy.Percent] = true
		if secretRelease && policy.Percent == 25 && !rotation.rotated {
			rotation.rotate(t, f, accepted)
		}
		if secretRelease && policy.Percent == 100 && !rotation.candidateCold {
			rotation.restartFresh(t, f, accepted.Builds[0].DeploymentID)
			rotation.candidateCold = true
		}
		if policy.Percent == 25 && !httpOnly && failureMode == "" && !rollback && contractMode == "" && !crashArmed {
			_, status := projectWorkerNativeRequest(t, "http://"+net.JoinHostPort(candidate.HostIP, "8080")+"/control/crash-next", "", http.MethodPost)
			if status != http.StatusNoContent {
				t.Fatalf("crash control status=%d", status)
			}
			crashArmed = true
		}
		if crashArmed && crashedJob == "" {
			// Evidence reads only: queue and gateway remain the sole delivery owners.
			err := f.h.Pool.QueryRow(t.Context(), `SELECT i.id::text, p.release_id::text, p.deployment_id::text
                FROM invocations i JOIN project_worker_release_pins p ON p.root_invocation_id=i.id
                WHERE p.deployment_id=$1 AND i.attempts>1 ORDER BY i.created_at LIMIT 1`, candidate.DeploymentID).Scan(&crashedJob, &crashedRelease, &crashedDeployment)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal(err)
			}
		}
		if !restarted {
			if policy.Percent != 5 {
				t.Fatal("initial progressive checkpoint is not 5 percent")
			}
			mustProjectWorkerNative(t, f.h.RestartSchedd())
			mustProjectWorkerNative(t, f.h.RestartAPID())
			fresh := state.NewPgStore(f.h.Pool)
			recovered, err := fresh.ProjectCanaryCheckpoint(t.Context(), f.accountID, attempt)
			mustProjectWorkerNative(t, err)
			if recovered.ID != policy.ID || recovered.Generation < policy.Generation {
				t.Fatal("daemon restart lost progressive checkpoint")
			}
			restarted = true
		}

		if strings.HasPrefix(failureMode, "retention_") && policy.Percent == 100 {
			f.qualifyRollbackRetention(t, attempt, failureMode, accepted, policy)
			return
		}
		if strings.HasPrefix(failureMode, "concurrent_") && (failureMode != "concurrent_full" || policy.Percent == 100) {
			f.qualifyConcurrentIntent(t, attempt, failureMode, accepted, policy)
			return
		}
		if failureMode == "missing_evidence" && !probeUnavailable {
			f.disableNativeProbes(t)
			probeUnavailable = true
		}
		if failureMode == "early_abort" && !controlSent || failureMode == "full_recover" && policy.Percent == 100 && !controlSent {
			if failureMode == "full_recover" {
				f.disableNativeProbes(t)
				probeUnavailable = true
			}
			controlReq = api.ProjectReleaseControlRequest{RequestID: strings.ReplaceAll(uuid.NewString(), "-", ""), ExpectedGeneration: policy.Generation}
			f.controlWithLostResponse(t, attempt, "abort", controlReq)
			controlSent = true
			continue
		}
		if failureMode == "full_recover" && policy.Status == "needs_operator" && !recoverySent {
			blocked := f.receipt(t, attempt, "rollback_failed")
			if blocked.Apply.SafeRelease.Verification.Control == nil || blocked.Apply.SafeRelease.Verification.Control.Status != "blocked" {
				t.Fatal("unknown baseline did not block operator restoration")
			}
			if time.Now().Before(policy.Deadline) {
				t.Fatal("operator recovery ended before its deadline")
			}
			f.assertNewWorkBlocked(t)
			mustProjectWorkerNative(t, f.h.SetAPIDEnv("FAAS_ROUTE_PROBE_URL", f.h.GatewayURL))
			mustProjectWorkerNative(t, f.h.RestartAPID())
			controlReq = api.ProjectReleaseControlRequest{RequestID: strings.ReplaceAll(uuid.NewString(), "-", ""), ExpectedGeneration: policy.Generation}
			f.controlWithLostResponse(t, attempt, "recover", controlReq)
			recoverySent = true
			continue
		}
		if failureMode != "" && policy.Status != "running" {
			want := "rolled_back"
			if failureMode == "missing_evidence" || failureMode == "low_traffic" {
				want = "rollback_failed"
			}
			final := f.receipt(t, attempt, want)
			safe := final.Apply.SafeRelease
			if failureMode == "missing_evidence" || failureMode == "low_traffic" {
				if time.Now().Before(policy.Deadline) || seen[25] || seen[100] || safe.Message == "" {
					t.Fatal("missing evidence promoted or ended without a bounded blocker")
				}
				f.assertNewWorkBlocked(t)
				if failureMode == "low_traffic" && (safe.Verification.WorkerHealth == nil || safe.Verification.WorkerHealth.Status != "insufficient_evidence") {
					t.Fatal("low traffic lacked real insufficient-evidence verdict")
				}
			} else {
				if !controlSent || safe.Verification.Control == nil || safe.Verification.Control.Status != "restored" {
					t.Fatal("operator decision was not restored")
				}
				if failureMode == "full_recover" && (!recoverySent || !seen[100]) {
					t.Fatal("full recovery was not exercised")
				}
				if failureMode == "early_abort" && (seen[25] || seen[100]) {
					t.Fatal("early abort advanced traffic")
				}
				f.assertHTTP(t, "stable", "stable-config")
			}
			f.assertGraph(t, f.baseline.ID)
			f.assertNoResubmission(t, accepted)
			if serviceGraph {
				t.Logf("service graph native: scenario=abort restart=true downstream=true status=%s", want)
			}
			t.Logf("progressive failure native: attempt=%s scenario=%s restart=true replay=%t status=%s http_only=%t", attempt, failureMode, controlSent, want, httpOnly)
			return
		}
		if failureMode == "full_recover" && controlSent {
			if !recoverySent {
				f.assertNewWorkBlocked(t)
			}
			select {
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			case <-time.After(time.Second):
			}
			continue
		}
		if policy.Percent < 100 {
			graph, err := f.store.ActiveProjectReleaseSet(t.Context(), f.accountID, f.project.ID, "production")
			mustProjectWorkerNative(t, err)
			latest, err := f.store.ProjectCanaryCheckpoint(t.Context(), f.accountID, attempt)
			mustProjectWorkerNative(t, err)
			if latest.Generation == policy.Generation && graph.ID != f.baseline.ID {
				t.Fatal("partial canary activated candidate graph")
			}
		} else if rollback && !regressed && (!stabilize || policy.Stabilizing) {
			// Target the candidate guest; cohort selection cannot regress baseline.
			for _, build := range accepted.Builds {
				if build.AppID != f.apps["frontend"].ID {
					continue
				}
				instances, err := f.store.ListInstancesForApp(t.Context(), build.AppID)
				mustProjectWorkerNative(t, err)
				for _, instance := range instances {
					if instance.DeploymentID != build.DeploymentID || instance.State != string(state.StateRunning) {
						continue
					}
					_, status := projectWorkerNativeRequest(t, "http://"+net.JoinHostPort(instance.HostIP, "8080")+"/control/regress", "", http.MethodPost)
					if status != http.StatusNoContent {
						t.Fatalf("regression control status=%d", status)
					}
					regressed = true
				}
			}
		}
		monitoring := false
		if stabilize && policy.Stabilizing && policy.Status == "complete" {
			receipt, err := f.owner.GetProjectSubmission(t.Context(), attempt)
			mustProjectWorkerNative(t, err)
			monitoring = api.ProjectReleaseMonitoring(receipt.Apply.SafeRelease)
			if rollback && !monitoring && receipt.Apply.SafeRelease.Status == "succeeded" {
				t.Fatal("stabilization ended without required native regression rollback")
			}
		}
		if policy.Status != "running" && !monitoring {
			want := "succeeded"
			if rollback {
				want = "rolled_back"
			}
			final := f.receipt(t, attempt, want)
			for _, percent := range []int{5, 25, 100} {
				if !seen[percent] {
					t.Fatalf("stage %d was never observed", percent)
				}
			}
			if !httpSeen["stable"] || !httpSeen["progressive"] {
				t.Fatal("native ingress did not exercise both graph cohorts")
			}
			if rollback {
				if !regressed {
					t.Fatal("rollback lacked a real candidate regression")
				}
				f.assertGraph(t, final.Apply.SafeRelease.RestoredReleaseID)
				f.assertHTTP(t, "stable", "stable-config")
			} else if contractMode == "" && !httpOnly {
				if !crashArmed || crashedJob == "" {
					t.Fatal("native worker crash did not produce a pinned retry")
				}
				inv, err := f.store.InvocationByID(t.Context(), crashedJob)
				mustProjectWorkerNative(t, err)
				var release, deployment string
				mustProjectWorkerNative(t, f.h.Pool.QueryRow(t.Context(), `SELECT release_id::text, deployment_id::text FROM project_worker_release_pins WHERE root_invocation_id=$1`, crashedJob).Scan(&release, &deployment))
				if release != crashedRelease || deployment != crashedDeployment {
					t.Fatal("worker retry changed immutable graph pin")
				}
				if inv.State != state.InvocationCompleted {
					t.Fatalf("crashed job did not recover: %+v", inv)
				}
				f.assertHTTP(t, "progressive", "progressive-config")
			}
			if contractMode != "" {
				f.qualifyMigrationContract(t, attempt, accepted, contractMode)
			}
			if httpOnly && !rollback {
				f.assertHTTP(t, "progressive", "progressive-config")
			}
			f.assertNoResubmission(t, accepted)
			if secretRelease {
				scenario := "promotion"
				if rollback {
					scenario = "rollback"
				}
				if !rotation.rotated || !rotation.baselineCold || !rotation.candidateCold {
					t.Fatal("native encryption key lifecycle was not exercised")
				}
				if rollback {
					rotation.restartFresh(t, f, f.baseline.Members[0].DeploymentID)
					f.assertHTTP(t, "stable", "stable-config")
				}
				t.Logf("release secrets native: scenario=%s stages=5,25,100 restart=true encrypted=true rekey=true old_key_removed=true cold_boot=true secret_config=matched status=%s", scenario, want)
			}
			if serviceGraph {
				scenario := "promotion"
				if rollback {
					scenario = "rollback"
				}
				t.Logf("service graph native: scenario=%s stages=5,25,100 restart=true downstream=true status=%s", scenario, want)
			}
			t.Logf("progressive native: attempt=%s stages=5,25,100 restart=true rollback=%t stabilization=%t status=%s http_only=%t", attempt, rollback, stabilize, want, httpOnly)
			return
		}
		// Continuous real jobs supply disjoint candidate health windows. At 5%
		// 20 jobs/second gives ample samples without fabricating evidence rows.
		for i := 0; !httpOnly && failureMode != "low_traffic" && i < 20; i++ {
			f.enqueue(t, uuid.NewString(), false)
		}
		body, status := projectWorkerNativeRequest(t, f.h.EdgeURL()+"/", f.apps["frontend"].Slug+".apps.test.example", http.MethodGet)
		var report projectWorkerNativeReport
		mustProjectWorkerNative(t, json.Unmarshal(body, &report))
		if status != http.StatusOK || report.Mode != report.Generation+"-config" {
			t.Fatalf("mixed native graph/config: %s", body)
		}
		if serviceGraph {
			assertNativeServiceGraphReport(t, report)
		}
		if secretRelease && !report.SecretMatchesGeneration {
			t.Fatal("native guest secret does not match its artifact generation")
		}
		httpSeen[report.Generation] = true
		if f.sql != nil {
			f.sql.observeReport(t, report)
		}
		for _, app := range f.apps {
			instances, err := f.store.ListInstancesForApp(t.Context(), app.ID)
			mustProjectWorkerNative(t, err)
			live := 0
			for _, instance := range instances {
				if instance.State == string(state.StateRunning) || instance.State == string(state.StateWaking) || instance.State == string(state.StateColdBooting) {
					live++
				}
			}
			if live > app.MaxConcurrency+api.RolloutConcurrencyGrant {
				t.Fatalf("native app exceeded rollout cap: %d", live)
			}
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("progressive native acceptance exceeded its bounded deadline")
}

func TestProjectProgressiveWorkerLowTrafficTimeoutMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, false, "low_traffic", "")
}
