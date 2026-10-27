// routes_contract_e2e_test.go — KVM-free acceptance for the route contract
// features: the OpenAPI contract preview and apply (declared vs observed
// routes, then gateway validation policy from the document), and saved route
// requirements (check, plan and apply a repair, then the gateway enforces it).
// Both run on the routes fixture: real apid, schedd and gateways over
// Postgres, a fake vmmd answering per path.

//go:build !no_pg

package e2e_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const contractDoc = `{"openapi":"3.1.0","info":{"title":"orders","version":"1"},"paths":{
"/items/{id}":{"get":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"integer"}}],"responses":{"200":{"description":"ok"}}}},
"/orders":{"post":{"requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","required":["qty"],"properties":{"qty":{"type":"integer"}}}}}},"responses":{"200":{"description":"ok"}}}}}}`

// post sends one anonymous JSON POST through the public edge, retrying the
// gateway's transient refusals as get does.
func (f *routesFixture) post(t *testing.T, path, body string) int {
	t.Helper()
	_, _, status := edgeRetry(t, http.MethodPost+" "+path, func() (http.Header, []byte, int) {
		return doReqHeaders(t, f.h, f.host, http.MethodPost, path, json.RawMessage(body), map[string]string{"Content-Type": "application/json"})
	})
	return status
}

func previewRoute(preview api.AppOpenAPIPolicyPreviewResponse, method, path string) (api.AppOpenAPIPolicyPreviewRoute, bool) {
	i := slices.IndexFunc(preview.Routes, func(r api.AppOpenAPIPolicyPreviewRoute) bool {
		return strings.EqualFold(r.Method, method) && r.Path == path
	})
	if i < 0 {
		return api.AppOpenAPIPolicyPreviewRoute{}, false
	}
	return preview.Routes[i], true
}

// TestE2E_Routes_OpenAPIContractPreviewAndValidationPolicy imports an OpenAPI
// document, sends real traffic, and reads the contract preview: declared and
// served routes match, a served route the document does not declare is
// observed only. Applying the reviewed plan creates validation rules from
// the document, the preview shows the route covered, and the gateway checks
// request bodies against the schema (observe mode: the app still answers).
func TestE2E_Routes_OpenAPIContractPreviewAndValidationPolicy(t *testing.T) {
	f := newRoutesFixture(t, "routes-contract")
	if f == nil {
		return
	}
	slug := f.app.Slug
	if status := f.api(t, http.MethodPost, "/v1/apps/"+slug+"/openapi", json.RawMessage(contractDoc), nil); status != http.StatusOK {
		t.Fatalf("import OpenAPI: status=%d", status)
	}
	f.respond("/items/42", http.StatusOK, `{"id":42}`)
	f.respond("/orders", http.StatusOK, `{"ok":true}`)
	f.respond("/legacy", http.StatusOK, "legacy")
	for range 5 {
		if _, _, status := f.get(t, "/items/42"); status != http.StatusOK {
			t.Fatalf("GET /items/42: status=%d", status)
		}
		if status := f.post(t, "/orders", `{"qty":1}`); status != http.StatusOK {
			t.Fatalf("POST /orders: status=%d", status)
		}
		if _, _, status := f.get(t, "/legacy"); status != http.StatusOK {
			t.Fatalf("GET /legacy: status=%d", status)
		}
	}

	var preview api.AppOpenAPIPolicyPreviewResponse
	deadline := time.Now().Add(60 * time.Second)
	for {
		if status := f.api(t, http.MethodGet, "/v1/apps/"+slug+"/openapi/preview", nil, &preview); status != http.StatusOK {
			t.Fatalf("GET preview: status=%d", status)
		}
		item, _ := previewRoute(preview, http.MethodGet, "/items/{id}")
		order, _ := previewRoute(preview, http.MethodPost, "/orders")
		legacy, _ := previewRoute(preview, http.MethodGet, "/legacy")
		if item.Status == "matched" && order.Status == "matched" && legacy.Status == "observed_only" {
			if order.Covered {
				t.Fatalf("POST /orders covered before any policy: %+v", order)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("preview did not show declared and observed routes within 60s: source=%q observed=%v routes=%+v",
				preview.Source, preview.ObservedAvailable, preview.Routes)
		}
		time.Sleep(2 * time.Second)
	}
	if !slices.ContainsFunc(preview.Suggestions, func(s api.EdgeRuleSuggestion) bool {
		return s.Kind == "validate" && slices.ContainsFunc(s.Methods, func(m string) bool { return strings.EqualFold(m, http.MethodPost) })
	}) {
		t.Fatalf("preview suggests no body validation for POST /orders: %+v", preview.Suggestions)
	}

	// Plan, then apply exactly the reviewed plan.
	var plan api.AppOpenAPIPolicyApplyResponse
	if status := f.api(t, http.MethodPost, "/v1/apps/"+slug+"/openapi/apply", api.ApplyAppOpenAPIPolicyRequest{}, &plan); status != http.StatusOK || !plan.Planned || plan.PreviewSHA256 == "" {
		t.Fatalf("plan OpenAPI policy: status=%d plan=%+v", status, plan)
	}
	var applied api.AppOpenAPIPolicyApplyResponse
	if status := f.api(t, http.MethodPost, "/v1/apps/"+slug+"/openapi/apply", api.ApplyAppOpenAPIPolicyRequest{
		Confirm: true, PreviewSHA256: plan.PreviewSHA256,
	}, &applied); status != http.StatusOK || applied.Planned || len(applied.Applied) == 0 {
		t.Fatalf("apply OpenAPI policy: status=%d applied=%+v", status, applied)
	}
	if status := f.api(t, http.MethodGet, "/v1/apps/"+slug+"/openapi/preview", nil, &preview); status != http.StatusOK {
		t.Fatalf("GET preview after apply: status=%d", status)
	}
	if order, _ := previewRoute(preview, http.MethodPost, "/orders"); !order.Covered {
		t.Fatalf("POST /orders not covered after apply: %+v", order)
	}

	// The gateway now validates bodies; observe mode counts the mismatch and
	// still forwards the request.
	deadline = time.Now().Add(60 * time.Second)
	for {
		if status := f.post(t, "/orders", `{"qty":"many"}`); status != http.StatusOK {
			t.Fatalf("POST /orders with an invalid body under observe mode: status=%d", status)
		}
		if n := validateFailures(f, "observe"); n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway counted no observe-mode validation failure within 60s: %v",
				metricLines(f.h.GatewayControlURL+"/metrics", "gateway_validate_failures_total"))
		}
		time.Sleep(time.Second)
	}
}

// validateFailures sums the gateway's validation failures for the fixture's
// app in mode.
func validateFailures(f *routesFixture, mode string) float64 {
	total := 0.0
	for _, line := range metricLines(f.h.GatewayControlURL+"/metrics", "gateway_validate_failures_total{") {
		if !strings.Contains(line, `app_id="`+f.app.ID+`"`) || !strings.Contains(line, `mode="`+mode+`"`) {
			continue
		}
		fields := strings.Fields(line)
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			total += v
		}
	}
	return total
}

// TestE2E_Routes_RequirementsRepairEnforcedByGateway saves route intent for
// a deployment's captured contract: every POST under /checkout/ is
// throttled, GET /health is a public exception. The check reports the
// missing throttle; a plan from the saved intent proposes it, apply commits
// it, the check then passes, and the gateway refuses requests beyond the
// planned rate.
func TestE2E_Routes_RequirementsRepairEnforcedByGateway(t *testing.T) {
	f := newRoutesFixture(t, "routes-requirements")
	if f == nil {
		return
	}
	slug, deployment := f.app.Slug, f.instance.DeploymentID
	doc := json.RawMessage(`{"openapi":"3.1.0","info":{"title":"checkout","version":"1"},"paths":{"/checkout/{id}":{"post":{"responses":{"200":{"description":"ok"}}}},"/health":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	if status := f.api(t, http.MethodPatch, "/v1/apps/"+slug+"/deployments/"+deployment+"/openapi",
		map[string]any{"set_doc": true, "doc": doc}, nil); status != http.StatusOK {
		t.Fatalf("capture deployment OpenAPI: status=%d", status)
	}
	maxRPS := 1.0
	zero := int64(0)
	var saved api.SavedRouteRequirements
	if status := f.api(t, http.MethodPut, "/v1/apps/"+slug+"/route-requirements", api.SaveRouteRequirementsRequest{
		ExpectedRevision: &zero,
		Requirements: api.RouteRequirementsConfig{
			Version: 2,
			Groups: []api.RouteGroup{{Name: "checkout", PathPrefix: "/checkout/", Methods: []string{http.MethodPost},
				Require: api.RouteChecks{Throttle: &api.RouteThrottleRequirement{KeyBy: api.ThrottleKeyByNone, MaxRPS: &maxRPS}}}},
			Public: []api.RoutePublicException{{Method: http.MethodGet, Path: "/health", Reason: "load balancer probe"}},
		},
	}, &saved); status != http.StatusOK || saved.Revision != 1 {
		t.Fatalf("save route requirements: status=%d saved=%+v", status, saved)
	}
	revision := saved.Revision
	check := func() api.RouteRequirementsCheck {
		t.Helper()
		var out api.RouteRequirementsCheck
		if status := f.api(t, http.MethodPost, "/v1/apps/"+slug+"/route-requirements/check",
			api.CheckRouteRequirementsRequest{DeploymentID: deployment, ExpectedRevision: &revision}, &out); status != http.StatusOK {
			t.Fatalf("check route requirements: status=%d", status)
		}
		return out
	}
	if before := check(); before.Report.Status != "violated" {
		t.Fatalf("check before the repair = %q, want violated: %+v", before.Report.Status, before.Report)
	}

	request := api.RoutePolicyPlanRequest{Saved: true, DeploymentID: deployment, ExpectedRevision: &revision, ThrottleBurst: 1}
	var plan api.RoutePolicyPlan
	if status := f.api(t, http.MethodPost, "/v1/apps/"+slug+"/route-policy/plan", request, &plan); status != http.StatusOK {
		t.Fatalf("plan repair: status=%d", status)
	}
	if plan.Status != "ready" || plan.SHA256 == "" || len(plan.Changes) == 0 {
		t.Fatalf("repair plan = %+v, want a ready plan with changes", plan)
	}
	var applied api.RoutePolicyApplyResponse
	if status := f.api(t, http.MethodPost, "/v1/apps/"+slug+"/route-policy/apply", api.RoutePolicyApplyRequest{
		RoutePolicyPlanRequest: request, ExpectedPlanSHA256: plan.SHA256, Confirm: true,
	}, &applied); status != http.StatusOK {
		t.Fatalf("apply repair: status=%d", status)
	}
	if after := check(); after.Report.Status != "satisfied" {
		t.Fatalf("check after the repair = %q, want satisfied: %+v", after.Report.Status, after.Report)
	}

	// The committed throttle reaches the gateway: a burst beyond one
	// request per second is refused, the app answers the rest.
	f.respond("/checkout/7", http.StatusOK, "ok")
	served, throttled := 0, 0
	deadline := time.Now().Add(60 * time.Second)
	for throttled == 0 || served == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("gateway did not enforce the planned throttle within 60s: served=%d throttled=%d receipt=%+v",
				served, throttled, applied.Receipt)
		}
		switch status := f.post(t, "/checkout/7", `{}`); status {
		case http.StatusOK:
			served++
		case http.StatusTooManyRequests:
			throttled++
		default:
			t.Fatalf("POST /checkout/7: status=%d", status)
		}
	}
	t.Logf("throttle: served=%d throttled=%d gateway_state=%s", served, throttled, applied.GatewayState)
}
