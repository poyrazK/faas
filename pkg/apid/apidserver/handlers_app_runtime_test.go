package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestCreateApp_ContainerRuntimeContract(t *testing.T) {
	for _, typ := range []string{"", "app"} {
		t.Run("type="+typ, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			for _, runtime := range []string{"go124", "node22", "invalid"} {
				rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "container-test", Type: typ, Runtime: runtime}, nil)
				apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
				if !strings.Contains(rec.Body.String(), "omit runtime") {
					t.Fatalf("missing recovery guidance: %s", rec.Body)
				}
			}
			// A rejected create must not consume the slug or quota.
			rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "container-test", Type: typ}, nil)
			if rec.Code != http.StatusCreated {
				t.Fatalf("container without runtime: %d %s", rec.Code, rec.Body)
			}
			var app api.AppResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
				t.Fatal(err)
			}
			if app.Type != "app" || app.Runtime != "" {
				t.Fatalf("container contract: type=%q runtime=%q", app.Type, app.Runtime)
			}
		})
	}
}
