// adr: 20261031095954 — the CLI deploys the Data API runtime apid serves.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestDataAPIRuntimeIsServedUnderTheDataAPICapability(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	apidserver.SeedAppTaskDeployment(t, e, "runtime-api")
	srv.SeedPlatformFlag(capabilityFlag(t, "managed-postgres"), dataAPITestReleased)
	srv.SeedPlatformFlag(capabilityFlag(t, "data-api"), dataAPITestHidden)
	if r := e.Do(t, http.MethodGet, "/v1/apps/runtime-api/data-api/runtime", nil, nil); !missingRoute(r) {
		t.Fatalf("runtime without the Data API = %d %s", r.Code, r.Body)
	}

	srv.SeedPlatformFlag(capabilityFlag(t, "data-api"), dataAPITestReleased)
	r := e.Do(t, http.MethodGet, "/v1/apps/runtime-api/data-api/runtime", nil, nil)
	var runtime api.DataAPIRuntime
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &runtime) != nil {
		t.Fatalf("runtime = %d %s", r.Code, r.Body)
	}
	want, err := templates.DataAPIRuntimeVersion()
	if err != nil || runtime.Version != want || runtime.Validate() != nil {
		t.Fatalf("runtime version %q, want %q (%v, %v)", runtime.Version, want, err, runtime.Validate())
	}
	if r := e.Do(t, http.MethodGet, "/v1/apps/someone-elses/data-api/runtime", nil, nil); r.Code != http.StatusNotFound {
		t.Fatalf("runtime for an unknown app = %d %s", r.Code, r.Body)
	}
}
