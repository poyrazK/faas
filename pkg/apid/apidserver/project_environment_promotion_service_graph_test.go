package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	promotionapi "github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
)

type promotionGraphSender struct {
	store                              *state.MemStore
	web, api, baselineWeb, baselineAPI string
	failCandidate                      bool
}

func (*promotionGraphSender) Configured() bool { return true }
func (s *promotionGraphSender) Probe(ctx context.Context, slug, id, token, method, path string) (routeprobe.Outcome, error) {
	return s.ProbeCritical(ctx, slug, id, token, method, path)
}
func (s *promotionGraphSender) ProbeCritical(ctx context.Context, _ string, id, _ string, _, _ string) (routeprobe.Outcome, error) {
	d, err := s.store.DeploymentByID(ctx, id)
	if err != nil {
		return routeprobe.Unattributed, err
	}
	if d.AppID == s.web {
		route, managed, found, err := s.store.ResolveProjectServiceBinding(ctx, s.web, id, "api")
		if err != nil || !managed || !found {
			return routeprobe.Unattributed, err
		}
		if id == s.baselineWeb && route.TargetDeploymentID != s.baselineAPI {
			return routeprobe.ServerError, nil
		}
		if id != s.baselineWeb && route.TargetDeploymentID == s.baselineAPI {
			return routeprobe.ServerError, nil
		}
		if s.failCandidate && id != s.baselineWeb {
			return routeprobe.ServerError, nil
		}
	}
	if d.AppID == s.api && s.failCandidate && id != s.baselineAPI {
		return routeprobe.ServerError, nil
	}
	return routeprobe.Success, nil
}

func TestPromotionServiceGraphAPI(t *testing.T) {
	oldDelay := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = oldDelay })
	for _, scenario := range []string{"healthy", "disabled", "requires_verify", "regression", "restart", "operator_edit"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			srv, store, account, project, backend, baseline := newPromotionHealthAPIFixture(t)
			web, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", Type: state.AppTypeApp,
				Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600, ServiceBindings: api.ServiceBindingsForTargets([]string{"api"})}})
			mustSnapshotPromotionAPI(t, err)
			var targetWeb state.Deployment
			for _, scope := range []string{"production", "staging"} {
				settings, err := state.WorkloadSettingsFromApp(web)
				mustSnapshotPromotionAPI(t, err)
				if scope == "staging" {
					settings.Manifest.ServiceBindingTransport = api.ServiceBindingTransportHTTPS
				}
				settings.Manifest.Env = api.ServiceBindingEnvForTransport(nil, settings.Manifest.ServiceBindings, settings.Manifest.ServiceBindingTransport)
				_, err = store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, scope, web.ID, 0, settings)
				mustSnapshotPromotionAPI(t, err)
				for key, value := range settings.Manifest.Env {
					mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(ctx, account.ID, web.ID, scope, key, value))
				}
				d, err := store.CreateDeployment(ctx, state.Deployment{AppID: web.ID, Scope: scope, Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat("f", 64)})
				mustSnapshotPromotionAPI(t, err)
				mustSnapshotPromotionAPI(t, store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/web-"+scope, "apps/web-"+scope, 42))
				mustSnapshotPromotionAPI(t, store.MarkDeploymentLive(ctx, d.ID))
				if scope == "staging" {
					targetWeb = d
				}
				active, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, scope)
				mustSnapshotPromotionAPI(t, err)
				members := append(active.Members, state.ProjectReleaseMember{AppID: web.ID, DeploymentID: d.ID})
				updated, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, scope, 1800, members)
				mustSnapshotPromotionAPI(t, err)
				if scope == "staging" {
					baseline = updated
				}
			}
			for _, app := range []state.App{backend, web} {
				gate, err := store.GetRouteHealthGate(ctx, account.ID, app.ID)
				mustSnapshotPromotionAPI(t, err)
				_, err = store.SetRouteHealthGate(ctx, account.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/database-health", Probe: &api.RouteHealthProbe{Path: "/database-health"}}}})
				mustSnapshotPromotionAPI(t, err)
			}
			baselineAPI := ""
			for _, member := range baseline.Members {
				if member.AppID == backend.ID {
					baselineAPI = member.DeploymentID
				}
			}
			sender := &promotionGraphSender{store: store, web: web.ID, api: backend.ID, baselineWeb: targetWeb.ID, baselineAPI: baselineAPI}
			srv.routeProbes = sender
			srv.WithPromotionHealthEnabled(true).WithPromotionServiceGraphEnabled(scenario != "disabled")
			plan, problem := srv.buildProjectEnvironmentPromotionPlan(ctx, account, project.Slug, "production", "staging", true, scenario != "requires_verify")
			if problem != nil {
				t.Fatal(problem)
			}
			if scenario == "disabled" || scenario == "requires_verify" {
				if plan.Preview.CanPromote {
					t.Fatal("unqualified graph promotion admitted")
				}
				return
			}
			if !plan.Preview.CanPromote || plan.Preview.DestinationConfigHash == "" {
				t.Fatalf("graph preview: %+v", plan.Preview)
			}
			body, _ := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: plan.Preview.PromotionToken})
			req, rec := projectRequest(http.MethodPost, "/promote", project.Slug, body)
			req.SetPathValue("environment", "staging")
			req.Header.Set("Idempotency-Key", "graph-health")
			srv.promoteProjectEnvironment(rec, req, account)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("admission=%d %s", rec.Code, rec.Body)
			}
			var accepted api.ProjectEnvironmentPromotionResponse
			mustSnapshotPromotionAPI(t, json.Unmarshal(rec.Body.Bytes(), &accepted))
			now := time.Now().UTC()
			store.SetClockForTest(func() time.Time { return now })
			job := state.PromotionHealthJob{AccountID: account.ID, ProjectSlug: project.Slug, ToEnvironment: "staging", PromotionID: accepted.PromotionID}
			run := func() {
				actions := srv.promotionHealthActions()
				actions.Now = func() time.Time { return now }
				mustSnapshotPromotionAPI(t, promotionapi.ReconcileHealth(ctx, store, job, actions))
			}
			run()
			now = now.Add(api.ProjectReleaseVerificationInterval)
			run()
			run() // cutover
			p, _, err := store.ProjectEnvironmentPromotionByID(ctx, account.ID, project.Slug, "staging", accepted.PromotionID)
			mustSnapshotPromotionAPI(t, err)
			if p.TargetReleaseSetID == "" {
				t.Fatal("healthy complete graph never published")
			}
			if scenario == "restart" {
				srv = newServer(store, srv.log, "gregale.dev", noopNotifier{})
				srv.routeProbes = sender
			}
			if scenario == "regression" {
				sender.failCandidate = true
			}
			if scenario == "operator_edit" {
				mustSnapshotPromotionAPI(t, store.UpsertAppEnvInScope(ctx, account.ID, web.ID, "staging", "OPERATOR", "new-intent"))
			}
			run()
			now = now.Add(api.ProjectReleaseVerificationInterval)
			for range 4 {
				run()
			}
			p, _, err = store.ProjectEnvironmentPromotionByID(ctx, account.ID, project.Slug, "staging", accepted.PromotionID)
			mustSnapshotPromotionAPI(t, err)
			if scenario == "regression" {
				if p.Status != "failed" || p.RollbackStatus != "rolled_back" {
					t.Fatalf("graph regression not restored: %+v", p)
				}
			} else if scenario == "operator_edit" {
				if p.Status != "failed" {
					t.Fatal("operator edit incorrectly verified")
				}
				values, err := store.ListAppEnvInScope(ctx, account.ID, web.ID, "staging")
				mustSnapshotPromotionAPI(t, err)
				found := false
				for _, row := range values {
					found = found || row.Key == "OPERATOR" && row.Value == "new-intent"
				}
				if !found {
					t.Fatal("operator edit overwritten")
				}
			} else if p.Status != "succeeded" {
				t.Fatalf("graph never completed: %+v", p)
			}
		})
	}
}
