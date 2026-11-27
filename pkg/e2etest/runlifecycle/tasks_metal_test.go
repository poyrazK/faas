//go:build metal

package runlifecycle_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func taskHTTP(t *testing.T, f *runFixture, method, path string, body any, status int) []byte {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(t.Context(), method, f.h.APIDURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.client.Token())
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	data, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != status {
		t.Fatalf("task route %s: HTTP %d %s (%v)", path, res.StatusCode, data, err)
	}
	return data
}
func TestSourceTaskContinuationMetal(t *testing.T)        { taskContinuationJourney(t, false) }
func TestSourceRunControlsContinuationMetal(t *testing.T) { taskContinuationJourney(t, true) }

func taskContinuationJourney(t *testing.T, controls bool) {
	t.Helper()
	f := newRunFixture(t)
	s := state.NewPgStore(f.h.Pool)
	app, err := s.CreateApp(t.Context(), state.App{AccountID: f.accountID, Slug: "report-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(t.Context(), state.ManagedRealtimeEndpoint{ID: uuid.NewString(), AccountID: f.accountID, AppID: app.ID, CallbackURL: "https://example.test/report", ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect", CallbackAuthTokenSealed: []byte("sealed"), AuthTokenSealed: []byte("sealed"), Enabled: true}, 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	def := runRequest(`import fs from 'node:fs'; export default async (input,c)=>{fs.writeFileSync(c.output_dir+'/report.txt','rows='+input.rows);return {rows:input.rows,uid:process.getuid()};}`)
	if controls {
		def.Source = strings.Replace(def.Source, "fs.writeFileSync", "await new Promise(resolve=>setTimeout(resolve,50));fs.writeFileSync", 1)
	}
	def.Input = nil
	def.OutputFiles = []string{"report.txt"}
	def.ArtifactStorage = api.ExecutionArtifactStored
	def.InputSchema = json.RawMessage(`{"type":"object","required":["rows"],"properties":{"rows":{"type":"integer"}}}`)
	def.ResultSchema = json.RawMessage(`{"type":"object","required":["rows","uid"],"properties":{"uid":{"const":1000},"rows":{"type":"integer"}}}`)
	version, err := f.client.PublishExecutionTask(t.Context(), api.PublishExecutionTaskRequest{Name: "report", Version: 1, Definition: def, AgentEndpointIDs: []string{endpoint.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var customerPolicy api.RunBudgetPolicy
	if controls {
		one, two := int64(1), int64(2)
		maximum := int64(version.Limits.MemoryMB+api.PerVMOverheadMB) * int64(version.Limits.TimeoutMS) * 2
		customerPolicy, err = f.client.SetRunBudget(t.Context(), "customer", "acceptance-customer", api.SetRunBudgetRequest{Enabled: true, Limits: api.RunBudgetLimits{MaxConcurrent: &one, MaxInvocationsPerDay: &two, MaxComputeMBMSPerDay: &maximum}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.client.SetRunAttributionBinding(t.Context(), "agent_endpoint", endpoint.ID, api.SetRunAttributionBindingRequest{CustomerRef: "acceptance-customer", AgentRef: "report-agent"}); err != nil {
			t.Fatal(err)
		}
		if _, err = f.client.SetRunBudget(t.Context(), "task", "report", api.SetRunBudgetRequest{Enabled: true, Limits: api.RunBudgetLimits{MaxInvocationsPerDay: &one}}); err != nil {
			t.Fatal(err)
		}
	}
	var callback struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(taskHTTP(t, f, "POST", "/v1/agent-callbacks", api.CreateAgentCallbackRequest{EndpointID: endpoint.ID, ThreadID: "report-thread", Kinds: []string{api.AgentCallbackKindToolResult}}, 201), &callback)
	if callback.URL == "" {
		t.Fatal("no signed callback URL")
	}
	invocation := map[string]any{"operation": "gregale_task", "id": "report-call", "call_id": "provider-call", "group_id": "report-thread", "callback_url": callback.URL, "arguments": map[string]any{"name": "report", "version": 1, "input": map[string]any{"rows": 3}}}
	if err = f.h.KillSchedd(); err != nil {
		t.Fatal(err)
	}
	taskHTTP(t, f, "POST", "/v1/agent-tools/invoke", invocation, 200)
	rows, err := s.ListExecutions(t.Context(), f.accountID, 10, 0)
	if err != nil || len(rows) != 1 || rows[0].Task == nil || rows[0].Task.ID != version.ID {
		history, readErr := s.ReadManagedRealtimeChannelHistory(t.Context(), endpoint.ID, api.AgentInboxChannel, 0, 1)
		detail := ""
		if readErr == nil && len(history.Messages) == 1 {
			var result struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(history.Messages[0].Data, &result)
			if strings.HasPrefix(result.Text, "gregale_task:") {
				detail = result.Text
			}
		}
		t.Fatal("task did not create one Run", err, len(rows), detail)
	}
	id := rows[0].ID
	f.runs[id] = true
	if controls {
		if rows[0].Attribution == nil || rows[0].Attribution.CustomerRef != "acceptance-customer" {
			t.Fatal("endpoint binding did not attribute the Run")
		}
		refused := map[string]any{}
		for key, value := range invocation {
			refused[key] = value
		}
		refused["id"] = "report-refused"
		refused["call_id"] = "provider-refused"
		for _, kind := range []string{"customer", "task"} {
			var problem api.Problem
			if json.Unmarshal(taskHTTP(t, f, "POST", "/v1/agent-tools/invoke", refused, 429), &problem) != nil || problem.RunBudget == nil || problem.RunBudget.Kind != kind {
				t.Fatal("wrong structured budget refusal")
			}
			if kind == "customer" {
				two := int64(2)
				customerPolicy.Limits.MaxConcurrent = &two
				customerPolicy, err = f.client.SetRunBudget(t.Context(), "customer", "acceptance-customer", api.SetRunBudgetRequest{ExpectedRevision: customerPolicy.Revision, Enabled: true, Limits: customerPolicy.Limits})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		customerPolicy, err = f.client.SetRunBudget(t.Context(), "customer", "acceptance-customer", api.SetRunBudgetRequest{ExpectedRevision: customerPolicy.Revision, Enabled: false, Limits: customerPolicy.Limits})
		if err != nil {
			t.Fatal(err)
		}
		pending, err := s.ReadManagedRealtimeChannelHistory(t.Context(), endpoint.ID, api.AgentInboxChannel, 0, 10)
		if err != nil || len(pending.Messages) != 0 {
			t.Fatal("refused calls produced terminal inbox messages", err)
		}
	}

	if err = f.client.RetireExecutionTask(t.Context(), "report", 1); err != nil {
		t.Fatal(err)
	}
	if err = f.h.SetAPIDEnv("FAAS_EXECUTION_TASKS_ENABLED", "0"); err != nil {
		t.Fatal(err)
	}
	if err = f.h.RestartAPID(); err != nil {
		t.Fatal(err)
	}
	// Same callback after an actual apid restart, writer rollback and retirement.
	taskHTTP(t, f, "POST", "/v1/agent-tools/invoke", invocation, 200)
	rows, err = s.ListExecutions(t.Context(), f.accountID, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatal("replay duplicated Run", err)
	}
	if err = f.h.RestartSchedd(); err != nil {
		t.Fatal(err)
	}
	row := f.wait(t, id, api.ExecutionStatusSucceeded)
	if row.ContractFailed() || row.ResultValidation == nil || len(row.Artifacts) != 1 || row.Artifacts[0].FileID == "" {
		t.Fatal("report result or artifact invalid")
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		history, err := s.ReadManagedRealtimeChannelHistory(t.Context(), endpoint.ID, api.AgentInboxChannel, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(history.Messages) > 0 {
			if len(history.Messages) != 1 || !strings.Contains(string(history.Messages[0].Data), id) {
				t.Fatal("duplicate or mismatched continuation")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion did not reach fresh agent inbox")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// A fresh control-plane reader serves the scoped receipt and artifact.
	// App workload ownership is covered by the callback authorization contracts.
	receipt := taskHTTP(t, f, "GET", "/v1/agent-tools/runs/"+id, nil, 200)
	if !bytes.Contains(receipt, []byte(`"rows":3`)) {
		t.Fatal("continuation receipt lacks structured result")
	}
	artifact := taskHTTP(t, f, "GET", "/v1/agent-tools/runs/"+id+"/artifacts/report.txt", nil, 200)
	if string(artifact) != "rows=3" {
		t.Fatal("continuation artifact mismatch")
	}
	if err = f.h.RestartAPID(); err != nil {
		t.Fatal(err)
	}
	taskHTTP(t, f, "POST", "/v1/agent-tools/invoke", invocation, 200)
	time.Sleep(6 * time.Second)
	history, err := s.ReadManagedRealtimeChannelHistory(t.Context(), endpoint.ID, api.AgentInboxChannel, 0, 10)
	if err != nil || len(history.Messages) != 1 {
		t.Fatal("second restart duplicated inbox continuation", err)
	}

	if controls {
		customer, err := f.client.GetRunBudget(t.Context(), "customer", "acceptance-customer")
		if err != nil || customer.Enabled || customer.Usage.Active != 0 || customer.Usage.Invocations != 1 || customer.Usage.ComputeReservedMBMS != 0 || customer.Usage.ComputeUsedMBMS <= 0 {
			t.Fatal("customer reservation did not settle across restart", err, customer.Usage)
		}
		task, err := f.client.GetRunBudget(t.Context(), "task", "report")
		if err != nil || task.Usage.ComputeUsedMBMS != customer.Usage.ComputeUsedMBMS || task.Usage.Invocations != 1 {
			t.Fatal("task allowance disagrees with customer usage", err)
		}
		page, err := f.client.ListRunHistory(t.Context(), api.RunHistoryFilter{TaskName: "report", TaskVersion: 1, CustomerRef: "acceptance-customer", AgentRef: "report-agent"})
		if err != nil || len(page.Runs) != 1 || page.Runs[0].ID != id || page.Runs[0].ResultValidationStatus != "valid" {
			t.Fatal("attributed history lost the completed task", err)
		}
		t.Logf("Run controls witness: budget_refusals=2 invocations=1 active=0 reserved_mb_ms=0 used_mb_ms=%d attribution=true history=true", customer.Usage.ComputeUsedMBMS)
	}
	f.requireWakeMethod(t, id, "cold_boot")
	f.assertSettled(t)
	t.Log(fmt.Sprintf("versioned task functional qualification: runs=1 inbox_records=1 apid_restart=true retired_replay=true writers_disabled=true structured_result=true stored_artifact=true version=%d", version.Version))
}
