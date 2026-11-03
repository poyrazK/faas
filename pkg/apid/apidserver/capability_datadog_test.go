// adr: 20261024222102 — creating a Datadog destination, or switching one to
// Datadog, needs the operator switch and the datadog-integration capability;
// other drains are untouched.
package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDatadogDestinationsNeedSwitchAndCapability(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "dd-api", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	datadog := api.CreateAppLogDrainRequest{Kind: api.AppLogDrainKindDatadog,
		TargetURL: "https://http-intake.logs.datadoghq.com/api/v2/logs", AuthHeader: "DD-API-KEY: abc"}
	create := func() (int, string) {
		r := e.Do(t, http.MethodPost, "/v1/apps/dd-api/log-drains", datadog, nil)
		return r.Code, r.Body.String()
	}

	if code, body := create(); code != http.StatusServiceUnavailable || !strings.Contains(body, "datadog_integration_unavailable") {
		t.Fatalf("datadog drain with the operator switch off = %d %s, want 503 datadog_integration_unavailable", code, body)
	}
	srv.SetDatadogIntegrationEnabled(true)
	if code, body := create(); code != http.StatusForbidden || !strings.Contains(body, api.CodeCapabilityNotReleased) {
		t.Fatalf("datadog drain with the capability off = %d %s, want capability_not_released", code, body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "datadog-integration"), platformflags.Definition{Enabled: true, Default: true})
	if code, body := create(); strings.Contains(body, api.CodeCapabilityNotReleased) || strings.Contains(body, "datadog_integration_unavailable") {
		t.Fatalf("datadog drain with switch and capability on was refused by the gate: %d %s", code, body)
	}

	srv.SetDatadogIntegrationEnabled(false)
	other := api.CreateAppLogDrainRequest{Kind: "http_json", TargetURL: "https://logs.example.com/ingest"}
	if r := e.Do(t, http.MethodPost, "/v1/apps/dd-api/log-drains", other, nil); strings.Contains(r.Body.String(), "datadog_integration_unavailable") {
		t.Fatalf("an http_json drain must not depend on the Datadog switch: %d %s", r.Code, r.Body)
	}
}

// adr: 20261024222102 — vmmd's runtime lines are indexed only for accounts
// the log-search capability is released to.
func TestRuntimeLinesFollowTheLogSearchCapability(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv, acct := *e.S(), *e.Acct()
	app := state.App{ID: uuid.NewString(), AccountID: acct.ID}
	ctx := context.Background()
	if !srv.LogSearchReleasedForApp(ctx, app) {
		t.Fatal("without a capability gate every account is released")
	}
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	if srv.LogSearchReleasedForApp(ctx, app) {
		t.Fatal("an internal capability must be off for an account until released")
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "log-search"), platformflags.Definition{Enabled: true, Default: true})
	if !srv.LogSearchReleasedForApp(ctx, app) {
		t.Fatal("a released capability must index the account's lines")
	}
	if srv.LogSearchReleasedForApp(ctx, state.App{ID: uuid.NewString(), AccountID: uuid.NewString()}) {
		t.Fatal("an account that cannot be read must decide off")
	}
}
