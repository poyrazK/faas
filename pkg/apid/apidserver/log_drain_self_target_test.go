package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// TestLogDrainRefusesItsOwnApp pins Hunt 32 H32-52: a drain to the app's own
// host turned every delivery into a request whose log lines were drained
// again (3,578 deliveries; the instance never parked).
func TestLogDrainRefusesItsOwnApp(t *testing.T) {
	t.Cleanup(hostidentity.Configure(hostidentity.Domains{Platform: "gregale.dev", Apps: "gregale.dev", Deploy: "gregale.dev"}))
	e := apidserver.Setup(t, api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "loop-api", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	const selfMessage = "cannot deliver to the app it drains"
	for _, target := range []string{"https://loop-api.gregale.dev/ready", "https://LOOP-API.gregale.dev./ingest"} {
		r := e.Do(t, http.MethodPost, "/v1/apps/loop-api/log-drains", api.CreateAppLogDrainRequest{Kind: "http_json", TargetURL: target}, nil)
		if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), selfMessage) {
			t.Fatalf("drain to %s = %d %s, want 400 %q", target, r.Code, r.Body, selfMessage)
		}
	}
	r := e.Do(t, http.MethodPost, "/v1/apps/loop-api/log-drains", api.CreateAppLogDrainRequest{Kind: "http_json", TargetURL: "https://other-api.gregale.dev/ingest"}, nil)
	if strings.Contains(r.Body.String(), selfMessage) {
		t.Fatalf("drain to another app was refused as a self target: %d %s", r.Code, r.Body)
	}
}
