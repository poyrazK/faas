package apidserver_test

// adr: 20261022153155 — an app's AI calls by deployment are served behind
// the AI gateway's gate, latest call first, with rates, cost per call and
// mean time to first byte.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOutboundAIDeploymentsRoute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.CreateApp(t, e, "ai-deploy-app")
	path := "/v1/apps/" + app.Slug + "/ai-deployments"
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, path, nil, nil), http.StatusForbidden, "outbound_ai_gateway_unavailable")
	(*e.S()).EnablePlatformFlagForAll(platformflags.AIGateway)

	now := time.Now().UTC().Truncate(time.Minute)
	earlier, latest := uuid.NewString(), uuid.NewString()
	(*e.Store()).SeedOutboundAIDeploymentStats(app.ID,
		state.OutboundAIDeploymentStats{DeploymentID: earlier, FirstMinute: now.Add(-5 * time.Hour), LastMinute: now.Add(-4 * time.Hour),
			Calls: 3, CostMillicents: 1_000},
		state.OutboundAIDeploymentStats{DeploymentID: latest, FirstMinute: now.Add(-time.Hour), LastMinute: now,
			Calls: 8, ErrorCalls: 2, FlaggedCalls: 1, CostMillicents: 803, InputTokens: 80, OutputTokens: 40, TTFBCalls: 3, TTFBMSTotal: 1_000})
	rec := e.Do(t, http.MethodGet, path, nil, nil)
	var list api.AIDeploymentUsageList
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Items) != 2 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	got, older := list.Items[0], list.Items[1]
	if got.DeploymentID != latest || got.Calls != 8 || got.ErrorRate != 0.25 || got.FlaggedRate != 0.125 || got.CostPerCallMillicents != 100 ||
		got.MeanTTFBMS == nil || *got.MeanTTFBMS != 333 || got.LastCallAt != now.Format(time.RFC3339) {
		t.Fatalf("latest = %+v", got)
	}
	if older.DeploymentID != earlier || older.CostPerCallMillicents != 333 || older.MeanTTFBMS != nil {
		t.Fatalf("earlier = %+v", older)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/apps/missing-app/ai-deployments", nil, nil), http.StatusNotFound, api.CodeNotFound)
}
