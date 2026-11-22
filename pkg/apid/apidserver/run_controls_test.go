package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRunControlsAPIAttributionBudgetsAndRollback(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	identity := apidserver.EnableExecutionAPIForTest(t, &e)
	oldIdentity, oldIdentities := *apidserver.MfaIdentity, *apidserver.MfaIdentities
	*apidserver.MfaIdentity = func() *age.X25519Identity { return identity }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.MfaIdentity, *apidserver.MfaIdentities = oldIdentity, oldIdentities })
	(*e.S()).WithExecutionV2Enabled(true).WithExecutionTasksEnabled(true)
	policyPath := "/v1/run-controls/budgets/customer/customer-42"
	if r := e.Do(t, http.MethodPut, policyPath, map[string]any{"expected_revision": 0, "enabled": true}, nil); r.Code != 400 {
		t.Fatal("incomplete replacement accepted", r.Code, r.Body.String())
	}
	one := int64(1)
	set := api.SetRunBudgetRequest{Enabled: true, Limits: api.RunBudgetLimits{MaxInvocationsPerDay: &one}}
	if r := e.Do(t, http.MethodPut, policyPath, set, nil); r.Code != 200 {
		t.Fatal("create policy", r.Code, r.Body.String())
	}
	if r := e.Do(t, http.MethodPut, policyPath, set, nil); r.Code != 409 {
		t.Fatal("stale policy", r.Code, r.Body.String())
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := (*e.Store()).CreateAPIKey(t.Context(), e.Acct().ID, hash, "bound-agent", []string{api.ScopeRunsRead, api.ScopeRunsWrite})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": "raw-first"}
	binding := api.SetRunAttributionBindingRequest{CustomerRef: "customer-42", AgentRef: "report-agent"}
	path := "/v1/run-controls/bindings/api_key/" + key.ID
	if r := e.Do(t, http.MethodPut, path, binding, nil); r.Code != 200 {
		t.Fatal("binding", r.Code, r.Body.String())
	}
	if r := e.Do(t, http.MethodGet, policyPath, nil, headers); r.Code != 403 {
		t.Fatal("narrow budget management", r.Code)
	}
	source := apidserver.ExecutionRequest()
	first := e.Do(t, http.MethodPost, "/v1/executions", source, headers)
	var rawRun api.ExecutionResponse
	if first.Code != 202 || json.Unmarshal(first.Body.Bytes(), &rawRun) != nil || rawRun.Attribution == nil || rawRun.Attribution.CustomerRef != "customer-42" {
		t.Fatal("bound source Run", first.Code, first.Body.String())
	}
	definition := source
	definition.Input = nil
	if r := e.Do(t, http.MethodPost, "/v1/run-tasks", api.PublishExecutionTaskRequest{Name: "report", Version: 1, Definition: definition}, nil); r.Code != 201 {
		t.Fatal("publish", r.Code, r.Body.String())
	}
	headers["Idempotency-Key"] = "task-after-source"
	invoke := api.InvokeExecutionTaskRequest{Name: "report", Version: 1, Input: json.RawMessage(`{"rows":3}`)}
	if r := e.Do(t, http.MethodPost, "/v1/run-tasks/invoke", invoke, headers); r.Code != 429 || !strings.Contains(r.Body.String(), "run_budget_exceeded") {
		t.Fatal("task bypassed raw-source customer usage", r.Code, r.Body.String())
	}
	if r := e.Do(t, http.MethodDelete, "/v1/executions/"+rawRun.ID, nil, headers); r.Code != 202 {
		t.Fatal("cancel", r.Code, r.Body.String())
	}
	two := int64(2)
	set.ExpectedRevision = 1
	set.Limits.MaxInvocationsPerDay = &two
	if r := e.Do(t, http.MethodPut, policyPath, set, nil); r.Code != 200 {
		t.Fatal("raise policy", r.Code, r.Body.String())
	}
	task := e.Do(t, http.MethodPost, "/v1/run-tasks/invoke", invoke, headers)
	var taskRun api.ExecutionResponse
	if task.Code != 202 || json.Unmarshal(task.Body.Bytes(), &taskRun) != nil || taskRun.Task == nil || taskRun.Attribution == nil || taskRun.Attribution.AgentRef != "report-agent" {
		t.Fatal("task attribution", task.Code, task.Body.String())
	}
	for _, extraScope := range [][]string{{api.ScopeRunsRead, api.ScopeRunsWrite}, {api.ScopeAppsRead, api.ScopeRunsWrite}} {
		otherToken, otherHash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (*e.Store()).CreateAPIKey(t.Context(), e.Acct().ID, otherHash, "unbound", extraScope); err != nil {
			t.Fatal(err)
		}
		spoof := invoke
		spoof.CustomerRef = "customer-42"
		if r := e.Do(t, http.MethodPost, "/v1/run-tasks/invoke", spoof, map[string]string{"Authorization": "Bearer " + otherToken, "Idempotency-Key": "spoof"}); r.Code != 403 {
			t.Fatal("unbound key selected customer", extraScope, r.Code, r.Body.String())
		}
	}
	history := e.Do(t, http.MethodGet, "/v1/run-history?customer_ref=customer-42&agent_ref=report-agent&task_name=report&task_version=1", nil, headers)
	var page api.RunHistoryResponse
	if history.Code != 200 || json.Unmarshal(history.Body.Bytes(), &page) != nil || len(page.Runs) != 1 || page.Runs[0].ID != taskRun.ID || strings.Contains(history.Body.String(), definition.Source) {
		t.Fatal("filtered history", history.Code, history.Body.String())
	}
	set.ExpectedRevision = 2
	set.Enabled = false
	if r := e.Do(t, http.MethodPut, policyPath, set, nil); r.Code != 200 {
		t.Fatal("kill switch", r.Code, r.Body.String())
	}
	replay := e.Do(t, http.MethodPost, "/v1/run-tasks/invoke", invoke, headers)
	if replay.Code != 202 || !strings.Contains(replay.Body.String(), taskRun.ID) || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal("disabled policy replay", replay.Code, replay.Body.String())
	}
	headers["Idempotency-Key"] = "after-kill"
	if r := e.Do(t, http.MethodPost, "/v1/executions", source, headers); r.Code != 429 {
		t.Fatal("kill bypass through source", r.Code, r.Body.String())
	}
	(*e.S()).WithExecutionV2Enabled(false)
	for _, path := range []string{policyPath, "/v1/run-history"} {
		if r := e.Do(t, http.MethodGet, path, nil, nil); r.Code != 200 {
			t.Fatal("writer rollback lost read", path, r.Code, r.Body.String())
		}
	}
	headers["Idempotency-Key"] = "task-after-source"
	if r := e.Do(t, http.MethodPost, "/v1/run-tasks/invoke", invoke, headers); r.Code != 202 {
		t.Fatal("writer rollback lost replay", r.Code, r.Body.String())
	}
	set.ExpectedRevision = 3
	if r := e.Do(t, http.MethodPut, policyPath, set, nil); r.Code != 200 {
		t.Fatal("writer rollback lost kill switch", r.Code, r.Body.String())
	}
}

func TestRunHistoryAPIPaginationAndScope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableExecutionAPIForTest(t, &e)
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := (*e.Store()).CreateAPIKey(t.Context(), e.Acct().ID, hash, "history", []string{api.ScopeRunsRead, api.ScopeRunsWrite})
	if err != nil {
		t.Fatal(err)
	}
	key, err = (*e.Store()).APIKeyByHash(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	request, problem := apidserver.ExecutionRequest().Resolve(api.PlanPro)
	if problem != nil {
		t.Fatal(problem)
	}
	for i := 0; i < api.RunHistoryPageMax+1; i++ {
		r, err := (*e.Store()).CreateExecution(t.Context(), state.CreateExecutionParams{AccountID: e.Acct().ID, RunsPrincipalID: &key.RunsPrincipalID, Request: request, SourceBytes: request.SourceBytes(), InputBytes: len(request.Input), AdmittedAt: base, DeadlineAt: base.Add(time.Duration(request.Limits.TimeoutMS) * time.Millisecond), SealedPayload: []byte("PRIVATE_SOURCE"), PayloadKID: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (*e.Store()).RequestExecutionCancellation(t.Context(), e.Acct().ID, r.ID, base.Add(time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	first := e.Do(t, http.MethodGet, "/v1/run-history?status=cancelled", nil, headers)
	var page api.RunHistoryResponse
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Runs) != api.RunHistoryPageMax || page.NextCursor == "" {
		t.Fatal("first page", first.Code, first.Body.String())
	}
	ids := map[string]bool{}
	for _, r := range page.Runs {
		ids[r.ID] = true
	}
	cursor := page.NextCursor
	page = api.RunHistoryResponse{}
	second := e.Do(t, http.MethodGet, "/v1/run-history?status=cancelled&cursor="+url.QueryEscape(cursor), nil, headers)
	if second.Code != 200 || json.Unmarshal(second.Body.Bytes(), &page) != nil || len(page.Runs) != 1 || ids[page.Runs[0].ID] || page.NextCursor != "" {
		t.Fatal("cursor tie breaker", second.Code, second.Body.String())
	}
	other, otherHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateAPIKey(t.Context(), e.Acct().ID, otherHash, "other", []string{api.ScopeRunsRead}); err != nil {
		t.Fatal(err)
	}
	r := e.Do(t, http.MethodGet, "/v1/run-history", nil, map[string]string{"Authorization": "Bearer " + other})
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Runs) != 0 {
		t.Fatal("cross-key history", r.Code, r.Body.String())
	}
	for _, query := range []string{"cursor=malformed", "since=yesterday", "task_version=1", "status=unknown", "status=queued&status=cancelled", "unexpected=1"} {
		if r := e.Do(t, http.MethodGet, "/v1/run-history?"+query, nil, headers); r.Code != 400 {
			t.Fatal("invalid query accepted", query, r.Code, r.Body.String())
		}
	}
}
