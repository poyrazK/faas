package apidserver_test

import (
	"encoding/json"
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPipelineAgentCompletionAcrossRestartAndRollback(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	identity := apidserver.EnableExecutionAPIForTest(t, &e)
	old, olds := *apidserver.MfaIdentity, *apidserver.MfaIdentities
	*apidserver.MfaIdentity = func() *age.X25519Identity { return identity }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.MfaIdentity, *apidserver.MfaIdentities = old, olds })
	s, store := *e.S(), *e.Store()
	s.WithExecutionV2Enabled(true).WithExecutionTasksEnabled(true).WithExecutionPipelinesEnabled(true)
	revocations := &memCallbackRevocations{at: map[string]time.Time{}}
	s.ConfigureAgentsForTest()
	s.SetAgentCallbackRevocationsForTest(revocations)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: e.Acct().ID, Slug: "pipeline-agent", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := store.CreateManagedRealtimeEndpointIfUnderQuota(t.Context(), state.ManagedRealtimeEndpoint{ID: uuid.NewString(), AccountID: e.Acct().ID, AppID: app.ID, Enabled: true}, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	definition := apidserver.ExecutionRequest()
	definition.Input = nil
	pub := api.PublishExecutionTaskRequest{Name: "agent-report", Version: 1, AgentEndpointIDs: []string{ep.ID}, Definition: definition}
	if r := e.Do(t, http.MethodPost, "/v1/run-tasks", pub, nil); r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	issue := e.Do(t, http.MethodPost, "/v1/agent-callbacks", api.CreateAgentCallbackRequest{EndpointID: ep.ID, ThreadID: "thread", Kinds: []string{api.AgentCallbackKindToolResult}}, nil)
	var callback api.AgentCallbackResponse
	if issue.Code != 201 || json.Unmarshal(issue.Body.Bytes(), &callback) != nil {
		t.Fatal(issue.Code, issue.Body.String())
	}
	invocation := map[string]any{"operation": "gregale_pipeline", "id": "call", "call_id": "provider-call", "group_id": "thread", "callback_url": callback.URL, "arguments": map[string]any{"batch": map[string]any{"name": "agent-report", "version": 1, "items": []any{map[string]any{"label": "report", "input": map[string]any{"private": "PRIVATE_INPUT"}}}}}}
	if r := e.Do(t, http.MethodPost, "/v1/agent-tools/invoke", invocation, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	claim, err := store.ClaimExecutionPipeline(t.Context(), "apid-first", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DriveExecutionPipelineForTest(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	run, err := store.ClaimExecutionForAccount(t.Context(), e.Acct().ID, "schedd", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.MarkExecutionRunning(t.Context(), run.ID, *run.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteExecution(t.Context(), state.CompleteExecutionParams{ID: run.ID, LeaseToken: *run.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"private":"PRIVATE_RESULT"}`), Usage: api.ExecutionUsage{WallTimeMS: 1}, FinishedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// Recreate the Agent handler before terminalization and delivery, preserving only durable stores.
	s.ConfigureAgentsForTest()
	s.SetAgentCallbackRevocationsForTest(revocations)
	claim, err = store.ClaimExecutionPipeline(t.Context(), "apid-restarted", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DriveExecutionPipelineForTest(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	s.WithExecutionPipelinesEnabled(false)
	if n, err := s.DeliverPipelineCallbacksForTest(t.Context()); err != nil || n != 1 {
		t.Fatal("delivery during rollback", n, err)
	}
	if n, err := s.DeliverPipelineCallbacksForTest(t.Context()); err != nil || n != 0 {
		t.Fatal("duplicate delivery", n, err)
	}
	history, err := store.ReadManagedRealtimeChannelHistory(t.Context(), ep.ID, api.AgentInboxChannel, 0, 100)
	if err != nil || len(history.Messages) != 1 {
		t.Fatal("durable inbox", len(history.Messages), err)
	}
	receiptPath := "/v1/agent-tools/pipelines/" + claim.ID
	receipt := e.Do(t, http.MethodGet, receiptPath, nil, nil)
	var parsed api.ExecutionPipelineReceipt
	if receipt.Code != 200 || json.Unmarshal(receipt.Body.Bytes(), &parsed) != nil || parsed.Completion == nil || parsed.Completion.CompletionID != claim.ID || strings.Contains(receipt.Body.String(), "PRIVATE_") {
		t.Fatal("manifest", receipt.Code, receipt.Body.String())
	}
	if r := e.Do(t, http.MethodGet, "/v1/agent-tools/runs/"+run.ID, nil, nil); r.Code != 200 || !strings.Contains(r.Body.String(), "PRIVATE_RESULT") {
		t.Fatal("scoped result", r.Code, r.Body.String())
	}
	if r := e.Do(t, http.MethodPost, "/v1/agent-tools/invoke", invocation, nil); r.Code != 200 {
		t.Fatal("replay", r.Code, r.Body.String())
	}
	runs, _ := store.ListExecutions(t.Context(), e.Acct().ID, 100, 0)
	if len(runs) != 1 {
		t.Fatal("replay admitted again")
	}
	if r := e.Do(t, http.MethodGet, receiptPath+"/events", nil, nil); r.Code != 200 || strings.Contains(r.Body.String(), "PRIVATE_") {
		t.Fatal("app progress", r.Code, r.Body.String())
	}
	if r := e.Do(t, http.MethodPost, "/v1/agent-callbacks/revocations", api.RevokeAgentCallbacksRequest{EndpointID: ep.ID, ThreadID: "thread"}, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, path := range []string{receiptPath, receiptPath + "/events", "/v1/agent-tools/runs/" + run.ID} {
		if r := e.Do(t, http.MethodGet, path, nil, nil); r.Code != 404 {
			t.Fatal("revoked receipt", path, r.Code, r.Body.String())
		}
	}
}
