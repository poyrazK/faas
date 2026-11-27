package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestStandaloneContractsRejectBeforeAdmissionAndReplayWithWriterDisabled(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.EnableExecutionAPIForTest(t, &e)
	(*e.S()).WithExecutionV2Enabled(true)
	r := apidserver.ExecutionRequest()
	r.InputSchema = json.RawMessage(`{"type":"integer"}`)
	r.Input = json.RawMessage(`"private-value"`)
	headers := map[string]string{"Idempotency-Key": "contracts"}
	bad := e.Do(t, http.MethodPost, "/v2/executions", r, headers)
	if bad.Code != 422 || strings.Contains(bad.Body.String(), "private-value") {
		t.Fatalf("input validation: %d %s", bad.Code, bad.Body.String())
	}
	runs, err := (*e.Store()).ListExecutions(context.Background(), e.Acct().ID, 10, 0)
	if err != nil || len(runs) != 0 {
		t.Fatal("invalid input admitted", err)
	}
	r.Input = json.RawMessage("42")
	r.ResultSchema = json.RawMessage(`{"type":"integer"}`)
	old := e.Do(t, http.MethodPost, "/v1/executions", r, headers)
	if old.Code != 400 {
		t.Fatalf("v1 accepted extended contract: %d", old.Code)
	}
	accepted := e.Do(t, http.MethodPost, "/v2/executions", r, headers)
	if accepted.Code != 202 {
		t.Fatalf("create: %d %s", accepted.Code, accepted.Body.String())
	}
	(*e.S()).WithExecutionV2Enabled(false)
	retry := e.Do(t, http.MethodPost, "/v2/executions", r, headers)
	if retry.Code != 202 || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("writer-disabled replay: %d %s", retry.Code, retry.Body.String())
	}
}
