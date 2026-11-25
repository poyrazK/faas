package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestLookupPipelineToolWorkflowRunStart(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "pipeline-start-lookup")
	other := seedWorkflowApp(t, e, "pipeline-start-lookup-other")
	key := "pipeline-tool:" + strings.Repeat("a", 64)
	headers := map[string]string{"Idempotency-Key": key}
	path := "/v1/apps/" + app.Slug + "/workflows/process-order/runs"
	lookup := "/v1/apps/" + app.Slug + "/workflows/run-starts/process-order"

	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, lookup, nil, nil), http.StatusBadRequest, api.CodeValidation)
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, lookup, nil, headers), http.StatusNotFound, api.CodeWorkflowRunNotFound)
	start := e.Do(t, http.MethodPost, path, map[string]string{"order_id": "retained"}, headers)
	if start.Code != http.StatusCreated {
		t.Fatalf("start = %d %s", start.Code, start.Body)
	}
	var accepted api.WorkflowRunResponse
	if err := json.Unmarshal(start.Body.Bytes(), &accepted); err != nil || accepted.ID == "" {
		t.Fatalf("accepted run = %+v, %v", accepted, err)
	}
	for range 2 {
		response := e.Do(t, http.MethodGet, lookup, nil, headers)
		var body map[string]string
		err := json.Unmarshal(response.Body.Bytes(), &body)
		if response.Code != http.StatusOK || err != nil || len(body) != 1 || body["run_id"] != accepted.ID || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("lookup = %d %s, %v", response.Code, response.Body, err)
		}
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/apps/"+other.Slug+"/workflows/run-starts/process-order", nil, headers), http.StatusNotFound, api.CodeWorkflowRunNotFound)
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/workflows/run-starts/w1", nil, headers), http.StatusNotFound, api.CodeWorkflowRunNotFound)
	runs, total, err := (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{Limit: 10})
	if err != nil || total != 1 || len(runs) != 1 {
		t.Fatalf("lookup must leave one run: %d %d %v", total, len(runs), err)
	}
}
