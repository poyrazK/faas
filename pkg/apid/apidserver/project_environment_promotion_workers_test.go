package apidserver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	promotionapi "github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019120040
func TestPromotionWorkerAPIContract(t *testing.T) {
	oldDelay := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = oldDelay })
	for _, scenario := range []string{"admission", "disabled", "requires_verify", "source_unqualified", "source_changed", "token_opt_in", "coordinated", "coordinated_migration_disabled", "worker_set", "coordinated_worker_set", "worker_set_last_unqualified"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			coordinated := strings.HasPrefix(scenario, "coordinated")
			srv, store, account, project, httpApp, _ := newPromotionHealthAPIFixture(t, coordinated, coordinated)
			_, err := store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "production", false)
			mustSnapshotPromotionAPI(t, err)
			multiple := strings.Contains(scenario, "worker_set")
			workers := []state.App{addPromotionAPIWorker(t, store, account, project, "worker", scenario, 0)}
			if multiple {
				mustSnapshotPromotionAPI(t, store.UpdateAccountPlan(ctx, account.ID, api.PlanScale))
				account.Plan = api.PlanScale
				lastScenario := scenario
				if scenario == "worker_set_last_unqualified" {
					lastScenario = "source_unqualified"
				}
				workers = append(workers, addPromotionAPIWorker(t, store, account, project, "mail", lastScenario, 1))
			}
			srv.WithPromotionHealthEnabled(true).WithPromotionWorkersEnabled(scenario != "disabled").WithPromotionMigrationsEnabled(scenario != "coordinated_migration_disabled")
			srv.routeProbes = promotionHealthAPISender{}
			gate, err := store.GetRouteHealthGate(ctx, account.ID, httpApp.ID)
			mustSnapshotPromotionAPI(t, err)
			_, err = store.SetRouteHealthGate(ctx, account.ID, httpApp.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/database-health", Probe: &api.RouteHealthProbe{Path: "/database-health"}}}})
			mustSnapshotPromotionAPI(t, err)
			plan, problem := srv.buildProjectEnvironmentPromotionPlan(ctx, account, project.Slug, "staging", "production", true, scenario != "requires_verify", coordinated, true)
			if problem != nil {
				t.Fatal(problem)
			}
			if scenario == "disabled" || scenario == "requires_verify" || scenario == "source_unqualified" || scenario == "source_changed" || scenario == "coordinated_migration_disabled" || scenario == "worker_set_last_unqualified" {
				if plan.Preview.CanPromote {
					t.Fatal("unqualified mixed promotion admitted", plan.Preview)
				}
				return
			}
			if !plan.Preview.CanPromote || plan.Preview.Health == nil || len(api.ProjectPromotionWorkers(plan.Preview.Health)) != len(workers) {
				t.Fatalf("missing worker contract: %+v", plan.Preview)
			}
			wire, err := decodeProjectEnvironmentPromotionToken(plan.Preview.PromotionToken)
			mustSnapshotPromotionAPI(t, err)
			if !wire.Workers || !wire.Verify || wire.Migrate != coordinated {
				t.Fatal("token omitted worker opt-in")
			}
			if scenario == "token_opt_in" {
				wire.Workers = false
				raw, _ := json.Marshal(wire)
				plan.Preview.PromotionToken = base64.RawURLEncoding.EncodeToString(raw)
			}
			body, _ := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: plan.Preview.PromotionToken})
			req, rec := projectRequest(http.MethodPost, "/promote", project.Slug, body)
			req.SetPathValue("environment", "production")
			req.Header.Set("Idempotency-Key", "worker-promotion-api")
			srv.promoteProjectEnvironment(rec, req, account)
			if scenario == "token_opt_in" {
				if rec.Code == http.StatusOK || rec.Code == http.StatusAccepted {
					t.Fatal("changed worker token accepted")
				}
				return
			}
			if rec.Code != http.StatusAccepted {
				t.Fatalf("admission=%d %s", rec.Code, rec.Body)
			}
			var accepted api.ProjectEnvironmentPromotionResponse
			mustSnapshotPromotionAPI(t, json.Unmarshal(rec.Body.Bytes(), &accepted))
			srv.WithPromotionWorkersEnabled(false).WithPromotionMigrationsEnabled(false)
			if multiple {
				req, rec := projectRequest(http.MethodPost, "/promote", project.Slug, body)
				req.SetPathValue("environment", "production")
				req.Header.Set("Idempotency-Key", "worker-promotion-api")
				srv.promoteProjectEnvironment(rec, req, account)
				if rec.Code != http.StatusAccepted {
					t.Fatalf("accepted worker set could not resume: %d %s", rec.Code, rec.Body)
				}
			}
			job := state.PromotionHealthJob{AccountID: account.ID, ProjectSlug: project.Slug, ToEnvironment: "production", PromotionID: accepted.PromotionID}
			mustSnapshotPromotionAPI(t, promotionapi.ReconcileHealth(ctx, store, job, srv.promotionHealthActions()))
			h, err := store.ProjectEnvironmentPromotionHealth(ctx, account.ID, job.PromotionID)
			mustSnapshotPromotionAPI(t, err)
			if coordinated {
				if h.Phase != "migrating" || len(api.ProjectPromotionWorkers(&h.Policy)) != len(workers) || h.Policy.Migration == nil || h.Migration.TaskID == "" {
					t.Fatalf("coordinated migration lost its contract: %+v", h)
				}
				for _, worker := range workers {
					held, err := store.HeldProjectReleaseWorkers(ctx, worker.ID)
					mustSnapshotPromotionAPI(t, err)
					if len(held) != 0 {
						t.Fatal("worker booted before migration success")
					}
				}
				return
			}
			if h.Phase != "preflight" || h.Verdict != "unknown" || len(api.ProjectPromotionWorkers(&h.Policy)) != len(workers) {
				t.Fatalf("candidate state=%+v", h)
			}
			progress := h.Workers
			if !multiple {
				progress = []api.ProjectPromotionWorkerHealth{{Worker: h.Worker, Health: h.WorkerHealth}}
			} else if h.Worker != nil || h.Policy.Worker != nil {
				t.Fatal("worker set exposed a singular approval alias")
			}
			if len(progress) != len(workers) {
				t.Fatal("worker progress omitted a member")
			}
			for _, member := range progress {
				if member.Worker == nil || member.Worker.DeploymentID == "" {
					t.Fatal("candidate worker missing")
				}
				held, err := store.HeldProjectReleaseWorkers(ctx, member.Worker.AppID)
				mustSnapshotPromotionAPI(t, err)
				if len(held) != 1 || held[0] != member.Worker.DeploymentID {
					t.Fatal("durable candidate was not held after admission disablement")
				}
			}
		})
	}
}

// adr: 20261019120040
func addPromotionAPIWorker(t *testing.T, store *state.MemStore, account state.Account, project state.Project, name, scenario string, index int) state.App {
	t.Helper()
	ctx := t.Context()
	worker, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-" + name, WorkloadName: name, Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600, ExecutionMode: api.ExecutionModeWorker, StopGracePeriodS: 30}})
	mustSnapshotPromotionAPI(t, err)
	_, err = store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: worker.ID, DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 2})
	mustSnapshotPromotionAPI(t, err)
	var source state.Deployment
	for _, scope := range []string{"staging", "production"} {
		mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(ctx, account.ID, worker.ID, scope, "MODE", scope))
		settings, err := state.MaterializeEnvironmentWorkloadSettings(ctx, store, worker, scope)
		mustSnapshotPromotionAPI(t, err)
		_, err = store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, scope, worker.ID, 0, settings)
		mustSnapshotPromotionAPI(t, err)
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: worker.ID, Scope: scope, Kind: state.DeploymentKindTarball})
		mustSnapshotPromotionAPI(t, err)
		mustSnapshotPromotionAPI(t, store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/worker", "layers/worker-"+scope, 4096))
		mustSnapshotPromotionAPI(t, store.MarkDeploymentLive(ctx, d.ID))
		graph, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, scope)
		mustSnapshotPromotionAPI(t, err)
		graph.Members = append(graph.Members, state.ProjectReleaseMember{AppID: worker.ID, DeploymentID: d.ID})
		_, err = store.PublishProjectReleaseSet(ctx, account.ID, project.ID, scope, 1800, graph.Members)
		mustSnapshotPromotionAPI(t, err)
		if scope == "staging" {
			source = d
		}
	}
	if scenario != "source_unqualified" {
		node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
		mustSnapshotPromotionAPI(t, err)
		ins, err := store.CreateInstanceWithMode(ctx, worker.ID, source.ID, string(state.StateColdBooting), 256, node.ID, uuid.NewString(), string(state.InstanceModeWorker))
		mustSnapshotPromotionAPI(t, err)
		snapshot, err := store.RuntimeAppValuesForDeployment(ctx, account.ID, worker.ID, source.ID)
		mustSnapshotPromotionAPI(t, err)
		fence, err := state.NewRuntimeAppConfigFence(snapshot)
		mustSnapshotPromotionAPI(t, err)
		inputs := state.RuntimeConfigInputs{Scope: "staging", Boundary: time.Now(), Variables: map[string]string{"MODE": "staging"}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}
		_, err = store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{AccountID: account.ID, AppID: worker.ID, InstanceID: ins.ID, NodeID: node.ID, WakeID: ins.WakeID, ExpectedState: ins.State, Netns: "source-" + name, HostIP: "10.100.0." + strconv.Itoa(8+index), GuestUID: 20008 + index, Fence: fence.SecretFence, ConfigFence: fence, Inputs: &inputs})
		mustSnapshotPromotionAPI(t, err)
	}
	if scenario == "source_changed" {
		mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(ctx, account.ID, worker.ID, "staging", "MODE", "untested"))
	}
	return worker
}
