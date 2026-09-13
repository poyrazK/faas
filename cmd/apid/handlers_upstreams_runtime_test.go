package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataUpstreamsRuntimeDisabledOnScale(t *testing.T) {
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "scale-upstreams-off")

	rec := e.do(t, http.MethodGet, "/v1/apps/scale-upstreams-off/upstreams", nil, nil)
	assertProblem(t, rec, http.StatusServiceUnavailable, api.CodeDataUpstreamsDisabled)
	if strings.Contains(rec.Body.String(), "upgrade to Hobby") {
		t.Fatalf("runtime-disabled response suggests a plan downgrade: %s", rec.Body.String())
	}
}
