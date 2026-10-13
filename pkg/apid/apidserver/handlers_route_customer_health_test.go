package apidserver_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteCustomerHealthAPIOptionsAndScopedUnavailableEvidence(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "customer-health")
	app, _ := (*e.Store()).AppBySlug(t.Context(), slug)
	d, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:test"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + slug + "/route-health/deployments/" + d.ID
	for _, query := range []string{"?customers=true", "?customers=true&customer_group_by=consumer&customer_details=true", ""} {
		rec := e.Do(t, "GET", path+query, nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%d: %s", rec.Code, rec.Body)
		}
		var report api.RouteHealthReport
		if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if query == "" {
			if report.Customers != nil {
				t.Fatal("unexpected customer query")
			}
			continue
		}
		if report.Customers == nil || report.Customers.Coverage != "observed_only" || report.Customers.Status != "disabled" {
			t.Fatal("missing explicit unavailable evidence")
		}
	}
	for _, query := range []string{"?customers=garbage", "?customers=true&customers=false", "?customer_details=true", "?customer_group_by=consumer", "?customers=true&customer_group_by=arbitrary", "?customers=true&customer_group_by=", "?customers=true&customer_details=garbage", "?customers=true&customer_group_by=tenant&customer_group_by=consumer"} {
		if rec := e.Do(t, "GET", path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("%s: %d %s", query, rec.Code, rec.Body)
		}
	}
	if rec := e.Do(t, "GET", "/v1/apps/"+slug+"/route-health/deployments/"+uuid.NewString()+"?customers=true", nil, nil); rec.Code != 404 {
		t.Fatal("unknown deployment leaked")
	}
	// Endpoint continues to use the existing apps:read boundary.
	key, hash, _ := api.GenerateAPIKey()
	if _, err := (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "read-health", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	(*e.Key()) = key
	if rec := e.Do(t, "GET", path+"?customers=true", nil, nil); rec.Code != 200 {
		t.Fatal("read scope denied")
	}
}

func TestRouteCustomerHealthOptionsDefaultAndValidation(t *testing.T) {
	for _, test := range []struct {
		query, group     string
		details, invalid bool
	}{{"?customers=true", "tenant", false, false}, {"?customers=true&customer_group_by=consumer&customer_details=true", "consumer", true, false}, {"?customers=false", "", false, false}, {"?customer_details=true", "", false, true}} {
		opts, err := apidserver.RouteCustomerHealthOptions(httptest.NewRequest("GET", "/"+test.query, nil))
		if (err != nil) != test.invalid || !test.invalid && (opts.CustomerGroupBy != test.group || opts.CustomerDetails != test.details) {
			t.Fatalf("%s: %+v %v", test.query, opts, err)
		}
	}
}
