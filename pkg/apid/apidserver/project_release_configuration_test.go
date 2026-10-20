package apidserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func pairedProjectRequest(t *testing.T, s *server, key, path, attempt string, raw []byte, migrate ...bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range map[string]string{"project_slug": "demo", "environment": "production", "no_triggers": "true", "release_config": string(raw)} {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("source", "source.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(pairedProjectArchive(t, len(migrate) > 0 && migrate[0])); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", path, &body)
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	mode := "atomic-config-verify"
	if len(migrate) > 0 && migrate[0] {
		mode = "atomic-config-migrate-verify"
	}
	if len(migrate) > 1 && migrate[1] {
		mode += "-workers"
	}
	request.Header.Set(api.ProjectSafeReleaseHeader, mode)
	if len(migrate) > 1 && migrate[1] {
		request.Header.Set(api.ProjectWorkerHealthHeader, "required")
	}
	if attempt != "" {
		request.Header.Set(api.DeploymentAttemptHeader, attempt)
	}
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, request)
	return rec
}
func TestPairedProjectApplyPinsConfigurationBeforeBuild(t *testing.T) {
	for _, variant := range []string{"http", "migration", "worker"} {
		t.Run(variant, func(t *testing.T) {
			migration := variant == "migration"
			workerRelease := variant == "worker"
			s, store, account, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
			initial := projectSecretsRequest(t, s, key, "/v1/projects", "", nil)
			if initial.Code != 200 {
				t.Fatalf("initial=%d %s", initial.Code, initial.Body)
			}
			var original api.ApplyResponse
			if err := json.Unmarshal(initial.Body.Bytes(), &original); err != nil {
				t.Fatal(err)
			}
			var members []state.ProjectReleaseMember
			for _, build := range original.Builds {
				app, err := store.MemStore.AppByID(t.Context(), build.AppID)
				if err != nil {
					t.Fatal(err)
				}
				app.Manifest.RevisionPinTTLSeconds = 3600
				if workerRelease && app.WorkloadName == "worker" {
					app.Manifest.ExecutionMode = api.ExecutionModeWorker
					app.Manifest.StopGracePeriodS = 30
				}
				if _, err := store.MemStore.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
					t.Fatal(err)
				}
				if err := store.MemStore.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "previous"); err != nil {
					t.Fatal(err)
				}
				old, err := store.MemStore.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MemStore.MarkDeploymentLive(t.Context(), old.ID); err != nil {
					t.Fatal(err)
				}
				members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: old.ID})
			}
			baseline, err := store.MemStore.PublishProjectReleaseSet(t.Context(), account.ID, original.ProjectID, "production", 1800, members)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.MemStore.UpdateProjectEnvironmentProtection(t.Context(), account.ID, original.ProjectID, "production", false); err != nil {
				t.Fatal(err)
			}
			if workerRelease {
				s.WithAtomicWorkerReleaseEnabled(true)
				var workerID string
				for _, row := range original.Builds {
					if row.Slug == "demo-worker" || strings.HasSuffix(row.Slug, "worker") {
						workerID = row.AppID
					}
				}
				if workerID == "" {
					t.Fatal("missing worker fixture")
				}
				if _, err := store.MemStore.CreateQueueBindingWithConsumer(t.Context(), state.QueueBinding{AccountID: account.ID, AppID: workerID, DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 2}); err != nil {
					t.Fatal(err)
				}
			}
			s.routeProbes = projectCriticalProbeSender{}
			zero := int64(0)
			if _, err := store.MemStore.SetRouteHealthGate(t.Context(), account.ID, members[0].AppID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/health", Probe: &api.RouteHealthProbe{Path: "/health"}}}}); err != nil {
				t.Fatal(err)
			}
			raw := []byte(`{"env":{"back":{"MODE":"api-new"},"worker":{"MODE":"worker-new"}}}`)
			scan := pairedProjectRequest(t, s, key, "/v1/projects/scan", "", raw, migration, workerRelease)
			if scan.Code != 200 {
				t.Fatalf("scan=%d %s", scan.Code, scan.Body)
			}
			var plan api.PlanResponse
			if err := json.Unmarshal(scan.Body.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if plan.SafeRelease == nil || plan.SafeRelease.Configuration == nil {
				t.Fatal("missing paired scan proof")
			}
			if workerRelease && (plan.SafeRelease.Verification == nil || plan.SafeRelease.Verification.WorkerHealth == nil) {
				t.Fatal("worker health requirement absent from admission policy")
			}
			admitted := 0
			store.onBuild = func(d state.Deployment) {
				admitted++
				runtime, err := store.MemStore.RuntimeAppValuesForDeployment(t.Context(), account.ID, d.AppID, d.ID)
				if err != nil {
					t.Fatal(err)
				}
				app, err := store.MemStore.AppByID(t.Context(), d.AppID)
				if err != nil {
					t.Fatal(err)
				}
				expected := "api-new"
				if app.WorkloadName == "worker" {
					expected = "worker-new"
				}
				if len(runtime.Values) != 1 || runtime.Values[0].Value != expected || !d.TrafficPercentExplicit || d.TrafficPercent != 0 {
					t.Fatalf("build admitted wrong config: %+v %+v", d, runtime.Values)
				}
				active, err := store.MemStore.ActiveProjectReleaseSet(t.Context(), account.ID, original.ProjectID, "production")
				if err != nil || active.ID != baseline.ID {
					t.Fatal("early code/config cutover")
				}
				if _, err := store.MemStore.ProjectEnvironmentConfigLatest(t.Context(), account.ID, original.ProjectID, "production"); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("configuration changed before candidates were ready")
				}
			}
			attempt := strings.Repeat("c", 32)
			response := pairedProjectRequest(t, s, key, "/v1/projects/safe-deploy?plan_token="+plan.PlanToken, attempt, raw, migration, workerRelease)
			if response.Code != 200 {
				t.Fatalf("apply=%d %s", response.Code, response.Body)
			}
			var apply api.ApplyResponse
			if err := json.Unmarshal(response.Body.Bytes(), &apply); err != nil {
				t.Fatal(err)
			}
			if admitted != 2 || apply.SafeRelease.Configuration.RequestHash != plan.SafeRelease.Configuration.RequestHash {
				t.Fatalf("admitted=%d response=%+v", admitted, apply)
			}
			for _, build := range apply.Builds {
				d, err := store.MemStore.DeploymentByID(t.Context(), build.DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				if (migration && len(d.ReleaseCommand) > 0) || (workerRelease && d.AppID == plan.SafeRelease.Worker.AppID) {
					if err := store.MemStore.SetDeploymentRootfs(t.Context(), d.ID, "/migration.ext4", "layers/"+d.ID, 4096); err != nil {
						t.Fatal(err)
					}
					if err := store.MemStore.PinDeploymentBuildDigest(t.Context(), d.ID, "/migration.ext4", "sha256:"+strings.Repeat("a", 64)); err != nil {
						t.Fatal(err)
					}
				}
				var stage state.StageState
				if err := json.Unmarshal(d.StageState, &stage); err != nil {
					t.Fatal(err)
				}
				if _, err := store.MemStore.AppendDeploymentStage(t.Context(), d.ID, stage.Current, state.StageReadiness, time.Now(), ""); err != nil {
					t.Fatal(err)
				}
				if err := store.MemStore.MarkDeploymentLive(t.Context(), d.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := store.MemStore.CloseDeploymentStage(t.Context(), d.ID, state.StageReadiness, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if workerRelease {
				pending, err := store.MemStore.ReconcileSafeProjectSubmission(t.Context(), account.ID, attempt)
				if err != nil || pending.Status != "building" {
					t.Fatalf("worker without runtime proof activated: %+v %v", pending, err)
				}
				var deploymentID string
				for _, row := range apply.Builds {
					if row.AppID == plan.SafeRelease.Worker.AppID {
						deploymentID = row.DeploymentID
					}
				}
				node, err := store.MemStore.CreateComputeNode(t.Context(), state.ComputeNode{Name: "worker-api-test", TargetURL: "unix:///tmp/worker-api-test.sock", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 4, Lifecycle: state.NodeLifecycleActive})
				if err != nil {
					t.Fatal(err)
				}
				instance, err := store.MemStore.CreateInstanceWithMode(t.Context(), plan.SafeRelease.Worker.AppID, deploymentID, string(state.StateColdBooting), 256, node.ID, uuid.NewString(), string(state.InstanceModeWorker))
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := store.MemStore.RuntimeAppValuesForDeployment(t.Context(), account.ID, plan.SafeRelease.Worker.AppID, deploymentID)
				if err != nil {
					t.Fatal(err)
				}
				fence, err := state.NewRuntimeAppConfigFence(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now(), Variables: map[string]string{"MODE": "worker-new"}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}
				if _, err := store.MemStore.PublishOwnedInstanceRuntime(t.Context(), state.RuntimeInstancePublication{AccountID: account.ID, AppID: plan.SafeRelease.Worker.AppID, InstanceID: instance.ID, NodeID: node.ID, WakeID: instance.WakeID, ExpectedState: instance.State, Netns: "fc-" + instance.ID, HostIP: "10.100.0.8", GuestUID: 20008, Fence: fence.SecretFence, ConfigFence: fence, Inputs: &inputs}); err != nil {
					t.Fatal(err)
				}
			}
			if migration {
				progress, err := store.MemStore.ReconcileSafeProjectSubmission(t.Context(), account.ID, attempt)
				if err != nil || progress.Status != "building" {
					t.Fatalf("missing task allowed cutover: %+v %v", progress, err)
				}
				var build api.AppliedBuild
				for _, row := range apply.Builds {
					if row.AppID == plan.SafeRelease.ReleaseCommand.AppID {
						build = row
					}
				}
				dep, err := store.MemStore.DeploymentByID(t.Context(), build.DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MemStore.SetDeploymentRootfs(t.Context(), dep.ID, "/migration.ext4", "layers/"+dep.ID, 4096); err != nil {
					t.Fatal(err)
				}
				task, err := store.MemStore.CreateAppTask(t.Context(), state.CreateAppTaskParams{AccountID: account.ID, AppID: dep.AppID, DeploymentID: dep.ID, Kind: state.AppTaskKindRelease, Command: dep.ReleaseCommand, CommandShell: dep.ReleaseCommandShell, CreatedAt: time.Now().UTC().Add(-20 * time.Second)})
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC().Add(-10 * time.Second)
				claimed, err := store.MemStore.ClaimNextAppTask(t.Context(), "migration", now, time.Minute)
				if err != nil || claimed.ID != task.ID {
					t.Fatalf("claim=%+v %v", claimed, err)
				}
				if _, err := store.MemStore.MarkAppTaskRunning(t.Context(), task.ID, *claimed.LeaseToken, now.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				zero := 0
				if _, err := store.MemStore.CompleteAppTask(t.Context(), state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &zero, FinishedAt: now.Add(2 * time.Second)}); err != nil {
					t.Fatal(err)
				}
				progress, err = store.MemStore.ReconcileSafeProjectSubmission(t.Context(), account.ID, attempt)
				if err != nil || progress.Status != "building" || !progress.ReleaseCommand.VerifyBaseline {
					t.Fatalf("migration success bypassed baseline probes: %+v %v", progress, err)
				}
				evidence := make([]api.ProjectReleaseProbeEvidence, 0, len(plan.SafeRelease.Verification.Probes))
				for _, probe := range plan.SafeRelease.Verification.Probes {
					evidence = append(evidence, api.ProjectReleaseProbeEvidence{Probe: probe, StableRequests: api.RouteHealthProbeRequestsPerMinute})
				}
				if _, err := store.MemStore.RecordProjectMigrationBaseline(t.Context(), account.ID, attempt, state.ProjectMigrationBaselineRound{TaskID: task.ID, StartedAt: time.Now().UTC(), Evidence: evidence}); err != nil {
					t.Fatal(err)
				}
			}
			progress, err := store.MemStore.ReconcileSafeProjectSubmission(t.Context(), account.ID, attempt)
			if err != nil || progress.Status != "verifying" || progress.Configuration.TargetVersion != 1 {
				t.Fatalf("progress=%+v err=%v", progress, err)
			}
			current, err := store.MemStore.ProjectEnvironmentConfigLatest(t.Context(), account.ID, original.ProjectID, "production")
			if err != nil || current.ConfigHash != progress.Configuration.TargetHash {
				t.Fatalf("paired head=%+v %v", current, err)
			}
			for _, member := range members {
				old, err := store.MemStore.RuntimeAppEnvForDeployment(t.Context(), account.ID, member.AppID, member.DeploymentID)
				if err != nil || old.Values[0].Value != "previous" {
					t.Fatal("retained release lost old variables")
				}
			}
		})
	}

}

func pairedProjectArchive(t *testing.T, migration bool) []byte {
	t.Helper()
	if !migration {
		return projectSecretArchive(t)
	}
	reader, err := gzip.NewReader(bytes.NewReader(projectSecretArchive(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	writer := tar.NewWriter(gz)
	source := tar.NewReader(reader)
	for {
		head, err := source.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteHeader(head); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(writer, source); err != nil {
			t.Fatal(err)
		}
	}
	body := []byte("release: /app/migrate --expand\n")
	if err := writer.WriteHeader(&tar.Header{Name: "Procfile", Mode: 0644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestProjectWorkerReleaseAdmissionDisabledByDefault(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/projects", nil)
	request.Header.Set(api.ProjectSafeReleaseHeader, "atomic-verify-workers")
	s := &server{}
	problem := s.prepareSafeProjectRelease(request, state.Account{}, &scanPlanRequest{}, false)
	if problem == nil || !strings.Contains(problem.Detail, "disabled pending native qualification") {
		t.Fatalf("worker admission should be disabled before accessing the store: %#v", problem)
	}
}

func TestProjectWorkerHealthRequiresWorkers(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/projects/scan", nil)
	request.Header.Set(api.ProjectSafeReleaseHeader, "atomic-verify")
	request.Header.Set(api.ProjectWorkerHealthHeader, "required")
	problem := (&server{}).prepareSafeProjectRelease(request, state.Account{}, &scanPlanRequest{}, false)
	if problem == nil || problem.Status != 400 {
		t.Fatalf("worker health without worker membership accepted: %+v", problem)
	}
}
