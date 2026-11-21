package apidserver_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"testing"
)

func TestExecutionDiagnosticEndpointAndRollbackRead(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableExecutionAPIForTest(t, &e)
	(*e.S()).WithExecutionV2Enabled(true)
	base := "registry.example/python@sha256:" + strings.Repeat("b", 64)
	recipe := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "six==1.17.0 --hash=sha256:" + strings.Repeat("a", 64)}
	digest, err := runenv.Digest(recipe, base)
	if err != nil {
		t.Fatal(err)
	}
	store := *e.Store()
	row, err := store.CreateExecutionEnvironment(t.Context(), state.ExecutionEnvironment{ID: uuid.NewString(), AccountID: e.Acct().ID, Runtime: recipe.Runtime, Recipe: recipe, RecipeDigest: digest, BaseRef: base})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecutionEnvironment(t.Context(), "build")
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := *e.Store()
	if err = diagnostics.AppendExecutionEnvironmentEvent(t.Context(), claim, api.ExecutionEnvironmentEvent{Stage: "build", Message: "installing"}); err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithExecutionEnvironments(false, nil)
	response := e.Do(t, http.MethodGet, "/v1/execution-environments/"+row.ID+"/events?after=0", nil, nil)
	var page api.ExecutionEnvironmentEventsResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Events) != 1 || page.NextSequence != 1 {
		t.Fatal(response.Code, response.Body.String())
	}
	if response = e.Do(t, http.MethodGet, "/v1/execution-environments/"+row.ID+"/events?after=-1", nil, nil); response.Code != 400 {
		t.Fatal("negative cursor", response.Code, response.Body.String())
	}
}
func TestRunCompletionReceiverWriterGate(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	request := accountReleaseWebhookReq()
	request.EventFilter = []string{api.ExecutionFinishedWebhookEvent}
	disabled := e.Do(t, http.MethodPost, accountReleaseWebhooksPath, request, nil)
	if disabled.Code == 201 {
		t.Fatal("disabled writer accepted receiver")
	}
	apidserver.EnableExecutionAPIForTest(t, &e)
	(*e.S()).WithExecutionV2Enabled(true)
	enabled := e.Do(t, http.MethodPost, accountReleaseWebhooksPath, request, nil)
	if enabled.Code != 201 {
		t.Fatal(enabled.Code, enabled.Body.String())
	}
	var hook api.AccountReleaseWebhookResponse
	if json.Unmarshal(enabled.Body.Bytes(), &hook) != nil {
		t.Fatal("invalid receipt")
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Store()).CreateAPIKey(t.Context(), e.Acct().ID, hash, "narrow-completion", []string{api.ScopeRunsRead, api.ScopeRunsWrite}); err != nil {
		t.Fatal(err)
	}
	request.TargetURL = "https://example.com/narrow-run-hook"
	if r := e.Do(t, http.MethodPost, accountReleaseWebhooksPath, request, map[string]string{"Authorization": "Bearer " + token}); r.Code != 403 {
		t.Fatal("runs-only key configured account receiver", r.Code, r.Body.String())
	}
	(*e.S()).WithExecutionV2Enabled(false)
	if r := e.Do(t, http.MethodGet, accountReleaseWebhooksPath+"/"+hook.ID, nil, nil); r.Code != 200 {
		t.Fatal("rollback hid receiver", r.Code)
	}
}
