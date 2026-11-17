package apidserver_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// flagEventsRuntime deploys one project app instance and returns a workload
// token for it and one platform tenant of the account.
func flagEventsRuntime(t *testing.T, plan api.Plan) (apidserver.TestEnv, string, string) {
	t.Helper()
	e := apidserver.Setup(t, plan)
	(*e.S()).EnablePlatformFlagForAll(platformflags.CustomerFlags)
	ctx := context.Background()
	acct := (*e.Acct()).ID
	project, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: acct, Slug: "flags-events", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: acct, ProjectID: project.ID, Slug: "flags-events-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "default"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := (*e.Store()).CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, "default-local", "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "flags-events", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if *(*e.S()).FlagsWorkloadVerifier(), err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer); err != nil {
		t.Fatal(err)
	}
	token, err := signer.Mint(time.Now(), acct, app.ID, instance.ID, workloadidentity.FlagsAudience)
	if err != nil {
		t.Fatal(err)
	}
	customer, _, err := (*e.Store()).CreatePlatformTenant(ctx, acct, "events-customer", "Events Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	return e, token.AccessToken, customer.ID
}

func TestFeatureFlagEventsRuntimeIngest(t *testing.T) {
	e, token, customer := flagEventsRuntime(t, api.PlanPro)
	auth := map[string]string{"Authorization": "Bearer " + token}
	post := func(t *testing.T, headers map[string]string, events []api.FlagEvent) (int, api.FlagEventsResponse, string) {
		t.Helper()
		res := e.Do(t, http.MethodPost, "/v1/runtime/flags/events", api.FlagEventsRequest{Events: events}, headers)
		var out api.FlagEventsResponse
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return res.Code, out, res.Body.String()
	}
	now := time.Now().UTC()

	code, out, body := post(t, auth, []api.FlagEvent{
		{Name: "order_completed", CustomerID: customer, Value: 1200},
		{Name: "order_completed", CustomerID: customer, At: now.Add(-time.Hour)},
		{Name: "order_completed", CustomerID: "6f1c1d8e-0000-4000-8000-000000000001"},
		{Name: "Bad Name", CustomerID: customer},
		{Name: "signup", CustomerID: customer, At: now.Add(-48 * time.Hour)},
		{Name: "signup", CustomerID: customer, At: now.Add(time.Hour)},
	})
	if code != http.StatusOK {
		t.Fatalf("ingest = %d %s", code, body)
	}
	want := api.FlagEventsResponse{Accepted: 2, Dropped: api.FlagEventsDropped{UnknownCustomer: 1, Invalid: 3},
		Month: api.FlagEventsMonth{Limit: api.PlanPro.FlagEventsPerMonth(), Accepted: 2}}
	if out != want {
		t.Fatalf("ingest = %+v; want %+v", out, want)
	}

	if code, _, body := post(t, nil, []api.FlagEvent{{Name: "signup", CustomerID: customer}}); code != http.StatusUnauthorized {
		t.Fatalf("no workload token = %d %s; want 401", code, body)
	}
	if code, _, body := post(t, auth, nil); code != http.StatusBadRequest || !strings.Contains(body, "1 to 100") {
		t.Fatalf("empty batch = %d %s; want 400", code, body)
	}
	many := make([]api.FlagEvent, api.FlagEventsMaxBatch+1)
	for i := range many {
		many[i] = api.FlagEvent{Name: "signup", CustomerID: customer}
	}
	if code, _, body := post(t, auth, many); code != http.StatusBadRequest {
		t.Fatalf("oversized batch = %d %s; want 400", code, body)
	}
}

// Free carries no request telemetry, so its quota is zero: events are
// dropped and counted, never stored or billed (ADR 20261115163113 §4).
func TestFeatureFlagEventsFreePlanQuotaIsZero(t *testing.T) {
	e, token, customer := flagEventsRuntime(t, api.PlanFree)
	res := e.Do(t, http.MethodPost, "/v1/runtime/flags/events", api.FlagEventsRequest{Events: []api.FlagEvent{{Name: "signup", CustomerID: customer}}},
		map[string]string{"Authorization": "Bearer " + token})
	var out api.FlagEventsResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || res.Code != http.StatusOK {
		t.Fatalf("free ingest = %d %s", res.Code, res.Body.String())
	}
	if out.Accepted != 0 || out.Dropped.Quota != 1 || out.Month.Limit != 0 || out.Month.Dropped != 1 {
		t.Fatalf("free ingest = %+v; want the event dropped over a zero quota", out)
	}
}
