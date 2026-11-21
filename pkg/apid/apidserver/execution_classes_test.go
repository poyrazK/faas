package apidserver_test

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"net/http"
	"testing"
)

func TestExtendedRunWriterGateDoesNotBreakReplay(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableExecutionAPIForTest(t, &e)
	(*e.S()).WithExecutionV2Enabled(true)
	request := apidserver.ExecutionRequest()
	request.Limits = &api.ExecutionLimitRequest{Class: api.ExecutionClassExtended, TimeoutMS: 300000}
	headers := map[string]string{"Idempotency-Key": "extended-gate"}
	disabled := e.Do(t, http.MethodPost, "/v2/executions", request, headers)
	if disabled.Code != 503 {
		t.Fatalf("disabled: %d %s", disabled.Code, disabled.Body.String())
	}
	rows, err := (*e.Store()).ListExecutions(context.Background(), e.Acct().ID, 10, 0)
	if err != nil || len(rows) != 0 {
		t.Fatal("disabled writer admitted work", err)
	}
	(*e.S()).WithExtendedExecutionsEnabled(true)
	accepted := e.Do(t, http.MethodPost, "/v2/executions", request, headers)
	if accepted.Code != 202 {
		t.Fatalf("accepted: %d %s", accepted.Code, accepted.Body.String())
	}
	var first api.ExecutionResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Limits.Class != api.ExecutionClassExtended || first.Limits.TimeoutMS != 300000 {
		t.Fatalf("receipt %+v", first)
	}
	(*e.S()).WithExtendedExecutionsEnabled(false)
	replay := e.Do(t, http.MethodPost, "/v2/executions", request, headers)
	if replay.Code != 202 {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
	var second api.ExecutionResponse
	_ = json.Unmarshal(replay.Body.Bytes(), &second)
	if second.ID != first.ID {
		t.Fatal("replay duplicated Run")
	}
}
