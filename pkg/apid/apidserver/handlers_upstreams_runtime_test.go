package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestDataUpstreamsRuntimeDisabledOnScale(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	apidserver.MustSeedApp(t, e, "scale-upstreams-off")

	rec := e.Do(t, http.MethodGet, "/v1/apps/scale-upstreams-off/upstreams", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusServiceUnavailable, api.CodeDataUpstreamsDisabled)
	if strings.Contains(rec.Body.String(), "upgrade to Hobby") {
		t.Fatalf("runtime-disabled response suggests a plan downgrade: %s", rec.Body.String())
	}
}
