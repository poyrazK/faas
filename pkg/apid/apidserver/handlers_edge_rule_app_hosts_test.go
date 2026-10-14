package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 1191 — "@app" is accepted on create and update; any other
// "@"-prefixed match_host is reserved.
func TestEdgeRuleAppWideMatchHost(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "app-wide")
	req := apidserver.EdgeRuleRouteReq(slug)
	req.MatchHost = api.EdgeRuleAppHosts
	if rec := e.Do(t, "POST", "/v1/apps/"+slug+"/edge-rules", req, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create @app: %d %s", rec.Code, rec.Body.String())
	}
	req.MatchHost = "@everything"
	if rec := e.Do(t, "POST", "/v1/apps/"+slug+"/edge-rules", req, nil); rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create @everything: %d, want 400/422", rec.Code)
	}
}
