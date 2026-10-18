package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type responseCheckPicker struct {
	target gateway.Target
	ready  bool
}

func (p *responseCheckPicker) PickForDeployment(app, deployment string) gateway.PickResult {
	return gateway.PickResult{OK: p.ready, Target: p.target}
}

func TestDeploymentResponseCheckGatewayContract(t *testing.T) {
	for _, outcome := range []string{"warm", "cold stage", "redirect", "large body", "wrong picker deployment", "retired during check", "caller headers", "extra JSON"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel, _ := reqbudget.WithRemaining(t.Context(), time.Minute, time.Minute, "forward", "private")
			defer cancel()
			store := state.NewMemStore()
			app := seedApp(t, store, "response-check", api.PlanPro)
			request := api.DeploymentResponseCheckRequest{AppID: app.ID, Scope: "default", Path: "/ready?temporary=1"}
			if outcome == "cold stage" {
				project, err := store.CreateProject(ctx, state.Project{AccountID: app.AccountID, Slug: "probe-project"})
				if err != nil {
					t.Fatal(err)
				}
				env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: app.AccountID, ProjectID: project.ID, Slug: "staging"})
				if err != nil {
					t.Fatal(err)
				}
				app, err = store.CreateApp(ctx, state.App{AccountID: app.AccountID, ProjectID: project.ID, Slug: "stage-check", Type: state.AppTypeApp, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				settings, err := state.WorkloadSettingsFromApp(app)
				if err != nil {
					t.Fatal(err)
				}
				settings.AppProtocol = api.AppProtocolHTTP2
				if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, app.AccountID, project.ID, "staging", app.ID, 0, settings); err != nil {
					t.Fatal(err)
				}
				request.AppID, request.ProjectID, request.EnvironmentID, request.Scope = app.ID, project.ID, env.ID, "staging"
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: request.Scope, Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, "probe-node", uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			picker := &responseCheckPicker{ready: outcome != "cold stage", target: gateway.Target{AppID: app.ID, DeploymentID: dep.ID, InstanceID: instance.ID, NodeID: instance.NodeID, WakeID: instance.WakeID}}
			if outcome == "wrong picker deployment" {
				picker.target.DeploymentID = uuid.NewString()
			}
			ensures, forwards := 0, 0
			checker := deploymentResponseChecker{store: store, picker: picker, appsDomain: "apps.example.test", ensure: func(_ context.Context, selected gateway.App, id string) error {
				ensures++
				if id != dep.ID || selected.ID != app.ID || selected.Scope != request.Scope {
					t.Error("wake changed the selected deployment or scope")
				}
				picker.ready = true
				return nil
			}, forward: func(target gateway.Target) http.Handler {
				if target != picker.target {
					t.Error("forward changed the selected replica")
				}
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					forwards++
					host := app.Slug + ".apps.example.test"
					if request.EnvironmentID != "" {
						host = gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, request.EnvironmentID, request.AppID)
					}
					if r.Method != http.MethodGet || r.URL.Path != "/ready" || r.URL.RawQuery != "temporary=1" || r.Host != host || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get(api.RevisionHeader) != "" {
						t.Error("forwarded credentials or changed the GET")
					}
					if r.Header.Get("x-faas-instance") != instance.ID || r.Header.Get("x-faas-app") != app.ID || r.Header.Get("x-faas-wake-id") != instance.WakeID {
						t.Error("forwarding bridge lost the selected replica identity")
					}
					if _, detachable := reqbudget.FromContext(r.Context()); detachable {
						t.Error("forwarding could detach the bounded check context")
					}
					if outcome == "cold stage" && r.Header.Get("x-faas-protocol") != api.AppProtocolHTTP2 {
						t.Error("forwarding used current app protocol instead of selected environment settings")
					}
					switch outcome {
					case "redirect":
						http.Redirect(w, r, "http://must-not-contact.example.test/", http.StatusFound)
					case "large body":
						_, _ = w.Write([]byte(strings.Repeat("x", api.DeploymentResponseCheckMaxBodyBytes+500)))
					case "retired during check":
						if err := store.MarkDeploymentSuperseded(r.Context(), dep.ID); err != nil {
							t.Error(err)
						}
						_, _ = w.Write([]byte("ready"))
					default:
						_, _ = w.Write([]byte("ready"))
					}
				})
			}}
			body, _ := json.Marshal(request)
			if outcome == "extra JSON" {
				body = append(body, []byte(` {"path":"/other"}`)...)
			}
			r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/internal/apps/"+app.Slug+"/response-check?deployment_id="+dep.ID, bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer account-secret")
			r.Header.Set("Cookie", "session=private")
			r.Header.Set(api.RevisionHeader, "another-deployment")
			w := httptest.NewRecorder()
			checker.ServeHTTP(w, r)
			wantOK := outcome != "wrong picker deployment" && outcome != "retired during check" && outcome != "extra JSON"
			if (w.Code == http.StatusOK) != wantOK || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("code=%d body=%s", w.Code, w.Body)
			}
			if wantOK {
				var result api.DeploymentResponseCheckResult
				if json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Matches(request, dep.ID) || result.BodyTruncated != (outcome == "large body") {
					t.Fatalf("invalid result: %s", w.Body)
				}
				if outcome == "redirect" && result.HTTPStatus != http.StatusFound {
					t.Fatal("redirect was followed or disguised")
				}
			}
			if outcome == "wrong picker deployment" || outcome == "extra JSON" {
				if forwards != 0 {
					t.Fatal("invalid request was forwarded")
				}
			} else if forwards != 1 || ensures != 1 {
				t.Fatalf("ensures=%d forwards=%d", ensures, forwards)
			}
		})
	}
}
