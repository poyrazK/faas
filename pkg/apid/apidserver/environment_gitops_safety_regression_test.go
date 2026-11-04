package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestGitOpsEmptyRetirementMeetsReleaseGuards(t *testing.T) {
	for _, scenario := range []string{"freeze", "two-approvals", "already-started"} {
		t.Run(scenario, func(t *testing.T) {
			gitOpsControlStores(t, func(t *testing.T, f gitOpsControlFixture) {
				// A live default deployment also counts as a production release.
				if scenario == "two-approvals" {
					deployment, err := f.Store.CreateDeployment(t.Context(), state.Deployment{AppID: f.App.ID, Scope: "default",
						Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("c", 64)})
					requireGitOpsControl(t, err)
					for _, stage := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
						requireGitOpsControl(t, f.Store.UpdateDeploymentStatus(t.Context(), deployment.ID, stage, ""))
					}
					requireGitOpsControl(t, f.Store.SetDeploymentRootfs(t.Context(), deployment.ID, "/tmp/guard-rootfs.ext4", "apps/"+f.App.ID+"/guard.ext4", 4096))
					requireGitOpsControl(t, f.Store.MarkDeploymentLive(t.Context(), deployment.ID))
					_, err = f.Store.(state.ReleaseApprovalStore).SetReleaseApprovalPolicy(t.Context(), state.ReleaseApprovalPolicy{
						AccountID: f.App.AccountID, AppID: f.App.ID, RequiredApprovals: 2, AfterStage: 1,
						TimeoutMinutes: 60, Approvers: state.ReleaseApproversAdmin, UpdatedBy: f.App.AccountID}, time.Now())
					requireGitOpsControl(t, err)
				}
				definition := f.Desired.Definition
				definition.Workloads = map[string]api.EnvironmentWorkload{}
				definition.WorkloadPruningPolicy = "retain"
				desired, err := environmentsync.Compile(definition)
				requireGitOpsControl(t, err)
				f.approve(t, desired)
				lease, err := f.Store.(state.EnvironmentGitOpsStore).ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
				requireGitOpsControl(t, err)
				backend := f.backend()
				_, err = backend.Apply(t.Context(), lease, f.plan(t, lease, desired))
				requireGitOpsControl(t, err)
				retirement := f.Store.(state.EnvironmentGitOpsWorkloadRetirementStore)
				var releaseID string
				if scenario == "already-started" {
					ready, err := retirement.ActivateEnvironmentGitOpsEmptyRetirement(t.Context(), lease, f.plan(t, lease, desired))
					requireGitOpsControl(t, err)
					if !ready {
						t.Fatal("fixture cutover did not commit")
					}
					release, err := f.Store.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), f.App.AccountID, f.App.ProjectID, "production")
					requireGitOpsControl(t, err)
					releaseID = release.ID
				}
				var freeze state.ReleaseFreeze
				if scenario != "two-approvals" {
					now := time.Now().UTC()
					freeze, err = f.Store.(state.ReleaseFreezeStore).CreateReleaseFreeze(t.Context(), state.ReleaseFreeze{
						AccountID: f.App.AccountID, AppID: f.App.ID, Kind: state.ReleaseFreezeWindow,
						StartsAt: &now, Reason: "incident hold", CreatedBy: f.App.AccountID}, api.ReleaseFreezesMax, now)
					requireGitOpsControl(t, err)
				}
				if scenario != "already-started" {
					// A partial graph must also check the member it removes, even
					// when no remaining member needs a candidate deployment.
					hold, err := f.Server.GitOpsReleaseHoldForTest(t.Context(), lease, state.EnvironmentWorkloadGraph{
						Members: []state.EnvironmentWorkloadGraphMember{{AppID: uuid.NewString()}}})
					requireGitOpsControl(t, err)
					if hold == "" {
						t.Fatal("release guard ignored a retiring graph member")
					}
				}
				for range 2 {
					ready, err := backend.(environmentgitops.RuntimeVerifier).VerifyRuntime(t.Context(), lease, f.plan(t, lease, desired))
					requireGitOpsControl(t, err)
					if ready != (scenario == "already-started") {
						t.Fatalf("guarded retirement readiness = %v", ready)
					}
				}
				rows, err := retirement.ListEnvironmentGitOpsWorkloadRetirements(t.Context(), f.App.AccountID, lease.Source.ID)
				requireGitOpsControl(t, err)
				want := "planned"
				if scenario == "already-started" {
					want = "retired"
				}
				if len(rows) != 1 || rows[0].Phase != want {
					t.Fatalf("guarded retirement phase: %+v", rows)
				}
				blocked, err := f.Store.(state.EnvironmentWorkloadRetirementAdmissionStore).EnvironmentWorkloadAdmissionRetired(t.Context(), f.App.ID, "production")
				requireGitOpsControl(t, err)
				if blocked != (scenario == "already-started") {
					t.Fatal("release hold changed admission before cutover")
				}
				release, err := f.Store.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), f.App.AccountID, f.App.ProjectID, "production")
				if scenario == "already-started" {
					requireGitOpsControl(t, err)
					if release.ID != releaseID {
						t.Fatal("cleanup published another release")
					}
					return
				}
				if !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("held retirement published a release: %s %v", release.ID, err)
				}
				if scenario == "freeze" {
					_, err = f.Store.(state.ReleaseFreezeStore).LiftReleaseFreeze(t.Context(), state.ReleaseFreezeOwner{AccountID: f.App.AccountID}, freeze.ID, f.App.AccountID, time.Now())
				} else {
					policy, readErr := f.Store.(state.ReleaseApprovalStore).ReleaseApprovalPolicy(t.Context(), f.App.ID)
					requireGitOpsControl(t, readErr)
					policy.RequiredApprovals = 1
					_, err = f.Store.(state.ReleaseApprovalStore).SetReleaseApprovalPolicy(t.Context(), policy, time.Now())
				}
				requireGitOpsControl(t, err)
				ready, err := backend.(environmentgitops.RuntimeVerifier).VerifyRuntime(t.Context(), lease, f.plan(t, lease, desired))
				requireGitOpsControl(t, err)
				if !ready {
					t.Fatal("retirement did not resume after its release hold ended")
				}
			})
		})
	}
}

type gitOpsRecoveryNotifier struct {
	Events    chan db.Notification
	Replies   []string
	Published []db.EdgeRuleChangedPayload
}

func (n *gitOpsRecoveryNotifier) Notify(_ context.Context, channel, raw string) error {
	if channel != db.NotifyEdgeRuleChanged {
		return nil
	}
	payload, err := db.ParseEdgeRuleChangedPayload(raw)
	if err != nil {
		return err
	}
	n.Published = append(n.Published, payload)
	for _, node := range n.Replies {
		ack, _ := json.Marshal(db.EdgeRuleAckPayload{Generation: payload.Generation, Phase: payload.Phase, Node: node})
		n.Events <- db.Notification{Channel: db.NotifyEdgeRuleAck, Payload: string(ack)}
	}
	return nil
}

func (n *gitOpsRecoveryNotifier) Subscribe(context.Context, []string) (<-chan db.Notification, func(), error) {
	return n.Events, func() {}, nil
}

func (*gitOpsRecoveryNotifier) WaitFor(context.Context, string, func(string) bool, time.Duration) (string, error) {
	return "", db.ErrWaitTimeout
}

func gitOpsPendingPolicyEffect(t *testing.T, f gitOpsControlFixture) (state.EnvironmentGitOpsLease, state.EnvironmentGitOpsEffect) {
	t.Helper()
	definition := f.Desired.Definition
	workload := definition.Workloads["api"]
	workload.Policies = &[]api.EnvironmentPolicy{{Name: "headers", Kind: "headers", Action: json.RawMessage(`{"response_headers":[{"name":"X-Test","action":"set","value":"approved"}]}`)}}
	definition.Workloads["api"] = workload
	desired, err := environmentsync.Compile(definition)
	requireGitOpsControl(t, err)
	source := f.approve(t, desired)
	intent := f.Store.(state.EnvironmentGitOpsIntentStore)
	adoption, err := intent.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
	requireGitOpsControl(t, err)
	if !adoption.CanApply() {
		t.Fatal("fixture policy adoption blocked")
	}
	requireGitOpsControl(t, intent.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, adoption.Hash))
	lease, err := f.Store.(state.EnvironmentGitOpsStore).ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
	requireGitOpsControl(t, err)
	generation, err := f.Store.(interface {
		NextEdgeRuleGeneration(context.Context) (int64, error)
	}).NextEdgeRuleGeneration(t.Context())
	requireGitOpsControl(t, err)
	effects := f.Store.(state.EnvironmentGitOpsEffectStore)
	_, err = effects.ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, f.plan(t, lease, desired), []state.EnvironmentGitOpsEffectSpec{{
		AppID: f.App.ID, Kind: "edge_policy", GatewayGeneration: generation, MatchHosts: []string{"example.test"}, ExpectedNodes: []string{"edge-a", "edge-b"}}})
	requireGitOpsControl(t, err)
	pending, err := effects.PendingEnvironmentGitOpsEffects(t.Context(), lease)
	requireGitOpsControl(t, err)
	if len(pending) != 1 {
		t.Fatalf("expected policy effect: %+v", pending)
	}
	return lease, pending[0]
}

func TestGitOpsPolicyRecoveryUsesDurableAndCurrentGatewayAcknowledgements(t *testing.T) {
	for _, scenario := range []string{"departed-ack", "departed-missing", "current-needs-ack", "new-gateway"} {
		t.Run(scenario, func(t *testing.T) {
			gitOpsControlStores(t, func(t *testing.T, f gitOpsControlFixture) {
				lease, effect := gitOpsPendingPolicyEffect(t, f)
				effects := f.Store.(state.EnvironmentGitOpsEffectStore)
				if scenario != "departed-missing" {
					requireGitOpsControl(t, effects.AcknowledgeEnvironmentGitOpsEffect(t.Context(), lease, effect.ID, effect.GatewayGeneration, "edge-a"))
				}
				nodes := []string{"edge-b"}
				if scenario == "new-gateway" {
					nodes = append(nodes, "edge-c")
				}
				for _, node := range nodes {
					role, target := "compute-node", "tcp://127.0.0.1:8080"
					_, err := f.Store.CreateComputeNode(t.Context(), state.ComputeNode{Name: node, Active: true, Role: &role,
						GatewayTargetURL: &target, TargetURL: "tcp://127.0.0.1:50051", AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5})
					requireGitOpsControl(t, err)
				}
				notifier := &gitOpsRecoveryNotifier{Events: make(chan db.Notification, 128), Replies: []string{"edge-b"}}
				if scenario == "current-needs-ack" {
					requireGitOpsControl(t, effects.AcknowledgeEnvironmentGitOpsEffect(t.Context(), lease, effect.ID, effect.GatewayGeneration, "edge-b"))
					notifier.Replies = nil
				}
				server := apidserver.NewServer(f.Store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", notifier)
				backend := server.EnvironmentGitOpsBackendForTest(f.Store.(state.EnvironmentGitOpsIntentStore), effects).(environmentgitops.EffectRecoverer)
				err := backend.RecoverEffects(t.Context(), lease)
				if (err == nil) != (scenario == "departed-ack") {
					t.Fatalf("recovery outcome: %v", err)
				}
				pending, readErr := effects.PendingEnvironmentGitOpsEffects(t.Context(), lease)
				requireGitOpsControl(t, readErr)
				if scenario == "departed-ack" {
					if len(pending) != 0 {
						t.Fatal("departed gateway's saved acknowledgement did not complete recovery")
					}
				} else {
					if len(pending) != 1 || !slices.Contains(pending[0].ExpectedNodes, "edge-a") {
						t.Fatal("recovery dropped the original minimum gateway roster", pending)
					}
					if scenario == "new-gateway" && !slices.Contains(pending[0].ExpectedNodes, "edge-c") {
						t.Fatal("new serving gateway was omitted from durable recovery")
					}
					if scenario != "departed-missing" {
						notifier.Replies = nodes
						requireGitOpsControl(t, backend.RecoverEffects(t.Context(), lease))
						pending, readErr = effects.PendingEnvironmentGitOpsEffects(t.Context(), lease)
						requireGitOpsControl(t, readErr)
						if len(pending) != 0 {
							t.Fatal("fresh serving-gateway acknowledgements did not finish recovery")
						}
					}
				}
				if len(notifier.Published) == 0 {
					t.Fatal("recovery did not replay policy invalidation")
				}
				for _, payload := range notifier.Published {
					if payload.AccountID != lease.Source.AccountID || payload.Generation != effect.GatewayGeneration || payload.Phase != "apply" {
						t.Fatalf("replay lost its original policy scope: %+v", payload)
					}
				}
			})
		})
	}
}

func TestGitOpsReadSurfacesRedactValuesAndPreserveInternalPlans(t *testing.T) {
	gitOpsControlStores(t, func(t *testing.T, f gitOpsControlFixture) {
		const hiddenValue = "original-private-console-value"
		requireGitOpsControl(t, f.Store.UpsertAppEnvInScope(t.Context(), f.App.AccountID, f.App.ID, "production", "UNMANAGED", hiddenValue))
		_, err := f.Store.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), f.App.AccountID, f.Lease.Source.ID,
			state.EnvironmentGitSourceUpdate{ExpectedGeneration: f.Lease.Source.Generation, Mode: "report"})
		requireGitOpsControl(t, err)
		worker := environmentgitops.Worker{Store: f.Store.(state.EnvironmentGitOpsStore), Backend: f.backend(), Mode: "report",
			LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
		worked, err := worker.RunOnce(t.Context())
		requireGitOpsControl(t, err)
		if !worked {
			t.Fatal("fixture report did not run")
		}
		token, hash, err := api.GenerateAPIKey()
		requireGitOpsControl(t, err)
		_, err = f.Store.CreateAPIKey(t.Context(), f.App.AccountID, hash, "gitops-read-only", []string{api.ScopeProjectEnvironmentRead})
		requireGitOpsControl(t, err)
		handler := f.Server.WithOpsMetrics(t.Context(), wire.NewOpsMetrics("gitops_redaction")).Handler()
		get := func(path string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, req)
			return out
		}
		if direct := get("/v1/apps/" + f.App.Slug + "/env?scope=production"); direct.Code != http.StatusForbidden {
			t.Fatalf("fixture key can read apps: %d", direct.Code)
		}
		status := get("/v1/projects/shop/environments/production/gitops")
		var public api.EnvironmentGitOpsStatusResponse
		if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &public) != nil || len(public.Runs) < 1 || strings.Contains(status.Body.String(), hiddenValue) {
			t.Fatalf("unsafe status response: %d %s", status.Code, status.Body)
		}
		stored, err := f.Store.(state.EnvironmentGitOpsStore).ListEnvironmentGitOpsRuns(t.Context(), f.App.AccountID, f.Lease.Source.ID, 1)
		requireGitOpsControl(t, err)
		if len(stored) != 1 || !strings.Contains(string(stored[0].Plan), hiddenValue) {
			t.Fatal("response redaction changed the internal report")
		}
		var internalPlan, publicPlan api.EnvironmentGitOpsPlan
		requireGitOpsControl(t, json.Unmarshal(stored[0].Plan, &internalPlan))
		requireGitOpsControl(t, json.Unmarshal(public.Runs[0].Plan, &publicPlan))
		if public.Runs[0].ID != stored[0].ID || publicPlan.Hash != internalPlan.Hash || publicPlan.ObservedVersion != internalPlan.ObservedVersion || len(publicPlan.Changes) != len(internalPlan.Changes) {
			t.Fatal("public report lost its reviewed identity")
		}
		preview := get("/v1/projects/shop/environments/production/gitops/adoption-preview")
		if preview.Code != http.StatusOK || strings.Contains(preview.Body.String(), hiddenValue) {
			t.Fatalf("unsafe adoption preview: %d %s", preview.Code, preview.Body)
		}
		var adoption api.EnvironmentGitOpsPlan
		requireGitOpsControl(t, json.Unmarshal(preview.Body.Bytes(), &adoption))
		plans := []api.EnvironmentGitOpsPlan{adoption}
		for _, run := range public.Runs {
			var plan api.EnvironmentGitOpsPlan
			requireGitOpsControl(t, json.Unmarshal(run.Plan, &plan))
			plans = append(plans, plan)
		}
		for _, plan := range plans {
			for _, change := range plan.Changes {
				if change.Before != nil || change.After != nil {
					t.Fatal("public plan returned customer values")
				}
			}
		}
		// A redacted receipt still authorizes exactly the original reviewed plan.
		requireGitOpsControl(t, f.Store.(state.EnvironmentGitOpsIntentStore).AdoptEnvironmentGitOps(t.Context(), f.App.AccountID, f.Lease.Source.ID, adoption.Hash))
	})
}
