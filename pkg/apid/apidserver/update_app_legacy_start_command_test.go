package apidserver_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// CLIs built before #4488 send "StartCommand": null on every app update.
// Once the field left the wire, apid rejected those updates with
// `json: unknown field "StartCommand"`, breaking `gregale app <slug>
// --no-require-authn`, scaling and redeploys for every older CLI.
func TestUpdateAppAcceptsLegacyNullStartCommand(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	apidserver.MustSeedApp(t, e, "legacy-cli")

	rec := e.Do(t, "PATCH", "/v1/apps/legacy-cli", json.RawMessage(`{"egress_ports":[5432],"StartCommand":null}`), nil)
	if rec.Code != 200 {
		t.Fatalf("legacy CLI update: status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !slices.Equal(out.EgressPorts, []int{5432}) {
		t.Fatalf("egress_ports = %v, want [5432]", out.EgressPorts)
	}

	rec = e.Do(t, "PATCH", "/v1/apps/legacy-cli", json.RawMessage(`{"StartCommand":"npm start"}`), nil)
	apidserver.AssertProblem(t, rec, 400, api.CodeValidation)
}
