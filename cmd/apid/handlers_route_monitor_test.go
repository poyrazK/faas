package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"gopkg.in/yaml.v3"
)

func TestRoutePolicyMonitoringWebhookSchema(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Items struct{ Enum []string }
				}
			}
		}
	}
	if err := yaml.Unmarshal(body, &spec); err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), api.AllowedAppWebhookEvents...)
	sort.Strings(want)
	for _, name := range []string{"CreateAppWebhookRequest", "UpdateAppWebhookRequest", "AppWebhookResponse"} {
		got := spec.Components.Schemas[name].Properties["event_filter"].Items.Enum
		sort.Strings(got)
		if !slices.Equal(got, want) {
			t.Fatalf("%s event vocabulary differs from server validation: %v, want %v", name, got, want)
		}
	}
}

func TestRoutePolicyMonitoringServerAndWebhookSubscription(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "monitored-routes")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	deployment, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:monitor", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	req := webhookReq()
	req.EventFilter = []string{"routes.requirements.violated", "routes.requirements.recovered"}
	hook := mustCreateWebhook(t, e, slug, req)
	base := "/v1/apps/" + slug + "/route-requirements"
	zero := int64(0)
	intent := api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: api.RouteRequirementsConfig{Version: 2,
		Groups: []api.RouteGroup{{Name: "health", PathPrefix: "/", Methods: []string{"GET"}, Require: api.RouteChecks{Authentication: "consumer"}}}}}
	if rec := e.do(t, "PUT", base, intent, nil); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	mode := api.ConsumerAuthModeRequired
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: &mode}); err != nil {
		t.Fatal(err)
	}
	doc := json.RawMessage(`{"openapi":"3.1.0","paths":{"/health":{"get":{}}}}`)
	if rec := e.do(t, "PATCH", "/v1/apps/"+slug+"/deployments/"+deployment.ID+"/openapi", map[string]any{"set_doc": true, "set_source": true, "doc": doc, "source": "manual_upload"}, nil); rec.Code != 200 {
		t.Fatalf("capture: %d %s", rec.Code, rec.Body)
	}
	resultPath := base + "/checks/" + deployment.ID
	drain := func(want string) {
		t.Helper()
		if n, err := e.s.drainAutomaticRouteChecks(t.Context()); err != nil || n != 1 {
			t.Fatalf("worker handoff: %d %v", n, err)
		}
		rec := e.do(t, "GET", resultPath, nil, nil)
		var result api.AutomaticRouteCheck
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.State != "complete" || result.Freshness != "current" || result.Check.Report.Status != want {
			t.Fatalf("current check: %d %s", rec.Code, rec.Body)
		}
	}
	drain("satisfied")
	if health, _ := e.store.AppWebhookEventOutboxHealth(t.Context()); health.PendingCount != 0 {
		t.Fatal("initial pass emitted recovery")
	}
	mode = api.ConsumerAuthModeOptional
	if rec := e.do(t, "PATCH", "/v1/apps/"+slug, api.UpdateAppRequest{ConsumerAuthMode: &mode}, nil); rec.Code != 200 {
		t.Fatalf("auth edit: %d %s", rec.Code, rec.Body)
	}
	drain("violated")
	if n, err := e.store.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatalf("violation intent: %d %v", n, err)
	}
	rows, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if err != nil || len(rows) != 1 || rows[0].Event != state.AppWebhookEventRouteRequirementsViolated || !strings.Contains(string(rows[0].Payload), resultPath) {
		t.Fatalf("violation: %+v %v", rows, err)
	}
	mode = api.ConsumerAuthModeRequired
	if rec := e.do(t, "PATCH", "/v1/apps/"+slug, api.UpdateAppRequest{ConsumerAuthMode: &mode}, nil); rec.Code != 200 {
		t.Fatalf("repair: %d %s", rec.Code, rec.Body)
	}
	drain("satisfied")
	if n, err := e.store.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatalf("recovery intent: %d %v", n, err)
	}
	rows, _, _ = e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if len(rows) != 2 {
		t.Fatalf("recovery lost: %+v", rows)
	}
}
