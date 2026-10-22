package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/internal/promotionrollouttest"
	"github.com/onebox-faas/faas/pkg/api"
	promotionapi "github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
)

type promotionHealthAPISender struct {
	unknown, fail bool
	baseline      string
}

func (promotionHealthAPISender) Configured() bool { return true }
func (s promotionHealthAPISender) Probe(ctx context.Context, slug, id, token, method, path string) (routeprobe.Outcome, error) {
	return s.ProbeCritical(ctx, slug, id, token, method, path)
}
func (s promotionHealthAPISender) ProbeCritical(_ context.Context, _ string, id string, token, method, path string) (routeprobe.Outcome, error) {
	if token == "" || method != "GET" || path != "/database-health" {
		return routeprobe.Unattributed, nil
	}
	if s.unknown {
		return routeprobe.Unauthenticated, nil
	}
	if s.fail && id != s.baseline {
		return routeprobe.ServerError, nil
	}
	return routeprobe.Success, nil
}

func newPromotionHealthAPIFixture(t *testing.T, migrate ...bool) (*server, *state.MemStore, state.Account, state.Project, state.App, state.ProjectReleaseSet) {
	t.Helper()
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	srv.promotionRollout = promotionrollouttest.New(t, promotionrollout.Target{AccountID: account.ID, ProjectID: project.ID}).Gate
	ctx := t.Context()
	mustSnapshotPromotionAPI(t, func() error {
		_, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
		return err
	}())
	app.Manifest.RevisionPinTTLSeconds = 3600
	app, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &app.Manifest})
	mustSnapshotPromotionAPI(t, err)
	create := func(scope, key string) state.Deployment {
		input := state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat("a", 64)}
		if scope == "staging" && len(migrate) > 1 && migrate[1] {
			input.ReleaseCommand = []string{"database expand --compatible"}
			input.ReleaseCommandShell = true
			input.ImageDigest = "sha256:" + strings.Repeat("e", 64)
		}
		d, err := store.CreateDeployment(ctx, input)
		mustSnapshotPromotionAPI(t, err)
		mustSnapshotPromotionAPI(t, store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+key, "apps/"+key, 42))
		mustSnapshotPromotionAPI(t, store.MarkDeploymentLive(ctx, d.ID))
		return d
	}
	mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "MODE", "old-production"))
	mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "staging", "MODE", "destination-variable"))
	old, previous := create("production", "old"), create("staging", "stage")
	baseline, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}})
	mustSnapshotPromotionAPI(t, err)
	target, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: previous.ID}})
	mustSnapshotPromotionAPI(t, err)
	raw := json.RawMessage(`{"env":{"api":{"MODE":"tested-variable"}}}`)

	configuration, err := store.PreviewProjectReleaseConfiguration(ctx, account.ID, project.ID, baseline.ID, raw)
	mustSnapshotPromotionAPI(t, err)
	safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Configuration: configuration}
	attempt := strings.Repeat("c", 32)
	_, err = store.ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("d", 64), api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, Environment: "production", SelectionComplete: true, Status: "complete", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}})
	mustSnapshotPromotionAPI(t, err)
	mustSnapshotPromotionAPI(t, store.StageProjectReleaseConfiguration(ctx, account.ID, attempt, project.ID, safe, raw))
	sourceCtx := state.ContextWithProjectReleaseConfigurationAttempt(state.ContextWithDeploymentSubmission(ctx, state.ProjectWorkloadAttempt(attempt, app.ID)), attempt)
	sourceInput := state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat("b", 64), TrafficPercentExplicit: true}
	if len(migrate) > 0 && migrate[0] && (len(migrate) < 2 || !migrate[1]) {
		sourceInput.ReleaseCommand = []string{"database expand --compatible"}
		sourceInput.ReleaseCommandShell = true
		sourceInput.ImageDigest = "sha256:" + strings.Repeat("e", 64)
	}
	source, err := store.CreateDeployment(sourceCtx, sourceInput)
	mustSnapshotPromotionAPI(t, err)
	mustSnapshotPromotionAPI(t, store.SetDeploymentRootfs(ctx, source.ID, "/rootfs/tested", "apps/tested", 84))
	mustSnapshotPromotionAPI(t, store.MarkDeploymentLiveDark(ctx, source.ID))
	_, err = store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: source.ID}})
	mustSnapshotPromotionAPI(t, err)
	values, hash, err := api.NormalizeProjectEnvironmentConfig(raw)
	mustSnapshotPromotionAPI(t, err)
	_, err = store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production", Values: values, ConfigHash: hash})
	mustSnapshotPromotionAPI(t, err)
	return srv, store, account, project, app, target
}

func TestPromotionHealthAPIAdmissionAndPreflight(t *testing.T) {
	oldDelay := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = oldDelay })
	for _, scenario := range []string{"healthy", "regression_before_cutover", "regression_after_cutover", "unauthenticated", "disabled", "changed_policy", "deadline", "restart_after_cutover", "operator_edit", "cutover_conflict", "admitted_policy", "post_cutover_deadline"} {
		t.Run(scenario, func(t *testing.T) {
			srv, store, account, project, app, baseline := newPromotionHealthAPIFixture(t)
			now := time.Now().UTC()
			store.SetClockForTest(func() time.Time { return now })
			srv.WithPromotionHealthEnabled(scenario != "disabled")
			srv.routeProbes = promotionHealthAPISender{unknown: scenario == "unauthenticated", fail: scenario == "regression_before_cutover", baseline: baseline.Members[0].DeploymentID}
			setProbe := func(path string) {
				gate, err := store.GetRouteHealthGate(t.Context(), account.ID, app.ID)
				mustSnapshotPromotionAPI(t, err)
				_, err = store.SetRouteHealthGate(t.Context(), account.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: path, Probe: &api.RouteHealthProbe{Path: path}}}})
				mustSnapshotPromotionAPI(t, err)
			}
			setProbe("/database-health")
			plan, problem := srv.buildProjectEnvironmentPromotionPlan(t.Context(), account, project.Slug, "production", "staging", true, true)
			if scenario == "disabled" {
				if problem != nil || plan.Preview.CanPromote {
					t.Fatal("unqualified health mode admitted")
				}
				return
			}
			if problem != nil || !plan.Preview.CanPromote || plan.Preview.Health == nil {
				t.Fatalf("health preview=%+v problem=%v", plan.Preview, problem)
			}
			plain, problem := srv.buildProjectEnvironmentPromotionPlan(t.Context(), account, project.Slug, "production", "staging", true)
			if problem != nil || plain.Preview.PromotionHash == plan.Preview.PromotionHash {
				t.Fatal("health contract absent from approval identity")
			}
			if scenario == "changed_policy" {
				setProbe("/other-health")
			}
			body, _ := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: plan.Preview.PromotionToken})
			req, rec := projectRequest(http.MethodPost, "/promote", project.Slug, body)
			req.SetPathValue("environment", "staging")
			req.Header.Set("Idempotency-Key", "health-api")
			srv.promoteProjectEnvironment(rec, req, account)
			if scenario == "changed_policy" {
				if rec.Code != http.StatusConflict {
					t.Fatalf("stale probe policy admitted: %d %s", rec.Code, rec.Body)
				}
				return
			}
			if rec.Code != http.StatusAccepted {
				t.Fatalf("admission=%d %s", rec.Code, rec.Body)
			}
			var accepted api.ProjectEnvironmentPromotionResponse
			mustSnapshotPromotionAPI(t, json.Unmarshal(rec.Body.Bytes(), &accepted))
			if accepted.Status != "running" {
				t.Fatal("pending health reported success")
			}
			p, workloads, err := store.ProjectEnvironmentPromotionByID(t.Context(), account.ID, project.Slug, "staging", accepted.PromotionID)
			mustSnapshotPromotionAPI(t, err)
			if workloads[0].Status != "pending" || p.TargetReleaseSetID != "" {
				t.Fatal("HTTP admission bypassed the leased worker")
			}
			job := state.PromotionHealthJob{AccountID: account.ID, ProjectSlug: project.Slug, ToEnvironment: "staging", PromotionID: p.ID}
			if scenario == "admitted_policy" {
				setProbe("/other-health")
			}
			run := func(server *server) {
				actions := server.promotionHealthActions()
				actions.Now = func() time.Time { return now }
				mustSnapshotPromotionAPI(t, promotionapi.ReconcileHealth(t.Context(), store, job, actions))
			}
			run(srv)
			h, err := store.ProjectEnvironmentPromotionHealth(t.Context(), account.ID, p.ID)
			mustSnapshotPromotionAPI(t, err)
			if h.Phase != "preflight" || len(h.Evidence) != 1 {
				t.Fatalf("preflight=%+v", h)
			}
			want := "healthy"
			if scenario == "regression_before_cutover" {
				want = "regressed"
			}
			if scenario == "unauthenticated" {
				want = "unknown"
			}
			if h.Verdict != want {
				t.Fatalf("verdict=%s want=%s", h.Verdict, want)
			}
			active, err := store.ActiveProjectReleaseSet(t.Context(), account.ID, project.ID, "staging")
			mustSnapshotPromotionAPI(t, err)
			if active.ID != baseline.ID {
				t.Fatal("one round moved production")
			}
			// A new server process uses the same lease, checkpoints and health deadline.
			restarted := newServer(store, srv.log, "gregale.dev", noopNotifier{})
			restarted.routeProbes = srv.routeProbes
			run(restarted)
			after, err := store.ProjectEnvironmentPromotionHealth(t.Context(), account.ID, p.ID)
			mustSnapshotPromotionAPI(t, err)
			if after.Generation != h.Generation || !after.Deadline.Equal(h.Deadline) {
				t.Fatal("restart reset the deadline or counted a premature round")
			}
			statusReq, statusRec := projectRequest(http.MethodGet, "/status", project.Slug, nil)
			statusReq.SetPathValue("environment", "staging")
			statusReq.SetPathValue("promotion", p.ID)
			restarted.getProjectEnvironmentPromotionStatus(statusRec, statusReq, account)
			var status api.ProjectEnvironmentPromotionStatusResponse
			mustSnapshotPromotionAPI(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
			if status.Health == nil || status.Status != "running" {
				t.Fatal("health checkpoint missing from public status")
			}
			if scenario == "unauthenticated" {
				return
			}
			if scenario == "deadline" {
				now = now.Add(api.ProjectReleaseVerificationTimeout)
				run(restarted)
				p, _, err = store.ProjectEnvironmentPromotionByID(t.Context(), account.ID, project.Slug, "staging", p.ID)
				mustSnapshotPromotionAPI(t, err)
				if p.Status != "failed" || p.TargetReleaseSetID != "" {
					t.Fatal("expired preflight published a candidate")
				}
				return
			}
			now = now.Add(api.ProjectReleaseVerificationInterval)
			run(restarted)
			h, err = store.ProjectEnvironmentPromotionHealth(t.Context(), account.ID, p.ID)
			mustSnapshotPromotionAPI(t, err)
			if scenario == "regression_before_cutover" {
				if h.Phase != "failed" {
					t.Fatal("failed preflight allowed cutover")
				}
				active, err = store.ActiveProjectReleaseSet(t.Context(), account.ID, project.ID, "staging")
				mustSnapshotPromotionAPI(t, err)
				if active.ID != baseline.ID {
					t.Fatal("failed preflight changed the destination")
				}
				return
			}
			if h.Phase != "cutover" {
				t.Fatalf("preflight did not qualify: %+v", h)
			}
			if scenario == "cutover_conflict" {
				mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "staging", "MODE", "cutover-edit"))
			}
			run(restarted)
			if scenario == "cutover_conflict" {
				h, err = store.ProjectEnvironmentPromotionHealth(t.Context(), account.ID, p.ID)
				mustSnapshotPromotionAPI(t, err)
				active, err = store.ActiveProjectReleaseSet(t.Context(), account.ID, project.ID, "staging")
				mustSnapshotPromotionAPI(t, err)
				if h.Phase != "conflicted" || active.ID != baseline.ID {
					t.Fatal("cutover conflict did not stop health-owned publication")
				}
				return
			}
			p, _, err = store.ProjectEnvironmentPromotionByID(t.Context(), account.ID, project.Slug, "staging", p.ID)
			mustSnapshotPromotionAPI(t, err)
			h, err = store.ProjectEnvironmentPromotionHealth(t.Context(), account.ID, p.ID)
			mustSnapshotPromotionAPI(t, err)
			if h.Phase != "verifying" || h.HealthyRounds != 0 || p.TargetReleaseSetID == "" {
				t.Fatal("cutover did not persist the new verification window")
			}
			if scenario == "restart_after_cutover" {
				restarted = newServer(store, srv.log, "gregale.dev", noopNotifier{})
				restarted.routeProbes = srv.routeProbes
			}
			if scenario == "regression_after_cutover" {
				restarted.routeProbes = promotionHealthAPISender{fail: true, baseline: baseline.Members[0].DeploymentID}
			}
			if scenario == "post_cutover_deadline" {
				restarted.routeProbes = promotionHealthAPISender{unknown: true}
				run(restarted)
				now = now.Add(api.ProjectReleaseVerificationTimeout)
				run(restarted)
				h, err = store.ProjectEnvironmentPromotionHealth(t.Context(), account.ID, p.ID)
				mustSnapshotPromotionAPI(t, err)
				p, _, err = store.ProjectEnvironmentPromotionByID(t.Context(), account.ID, project.Slug, "staging", p.ID)
				mustSnapshotPromotionAPI(t, err)
				if h.Phase != "rollback_failed" || p.Status != "failed" || p.RollbackStatus != "" {
					t.Fatal("unknown baseline health authorized automatic rollback")
				}
				return
			}

			run(restarted)
			now = now.Add(api.ProjectReleaseVerificationInterval)
			run(restarted)
			if scenario == "operator_edit" {
				mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "staging", "MODE", "operator-value"))
			}
			run(restarted)
			if scenario == "operator_edit" {
				run(restarted)
			}
			p, _, err = store.ProjectEnvironmentPromotionByID(t.Context(), account.ID, project.Slug, "staging", p.ID)
			mustSnapshotPromotionAPI(t, err)
			active, err = store.ActiveProjectReleaseSet(t.Context(), account.ID, project.ID, "staging")
			mustSnapshotPromotionAPI(t, err)
			switch scenario {
			case "regression_after_cutover":
				if p.Status != "failed" || p.RollbackStatus != "rolled_back" || active.Members[0].DeploymentID != baseline.Members[0].DeploymentID {
					t.Fatalf("automatic restore failed: %+v", p)
				}
				values, err := store.RuntimeAppValuesForDeployment(t.Context(), account.ID, app.ID, baseline.Members[0].DeploymentID)
				mustSnapshotPromotionAPI(t, err)
				if len(values.Values) != 1 || values.Values[0].Value != "destination-variable" {
					t.Fatal("rollback lost previous runtime variables")
				}
			case "operator_edit":
				values, err := store.ListAppEnvInScope(t.Context(), account.ID, app.ID, "staging")
				mustSnapshotPromotionAPI(t, err)
				if p.Status != "failed" || values[0].Value != "operator-value" {
					t.Fatal("automatic recovery overwrote newer destination intent")
				}
			default:
				if p.Status != "succeeded" || p.VerificationStatus != "verified" || active.ID != p.TargetReleaseSetID {
					t.Fatalf("healthy release not completed: %+v", p)
				}
			}

		})
	}
}
