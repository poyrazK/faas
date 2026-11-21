package apidserver_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestAgentExecutionFileGrantAdmissionAndReplay(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	identity, restore := apidserver.WithTestIdentities(t)
	defer restore()
	s := *e.S()
	s.WithExecutionAPIEnabled(true).WithExecutionV2Enabled(true).WithExecutionTasksEnabled(true)
	files, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.WithExecutionFileStorage(files)
	revocations := &memCallbackRevocations{at: make(map[string]time.Time)}
	s.ConfigureAgentsForTest()
	s.SetAgentCallbackRevocationsForTest(revocations)
	ctx, store := t.Context(), *e.Store()
	account := e.Acct().ID
	app, err := store.CreateApp(ctx, state.App{AccountID: account, Slug: "scoped-report", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := func() state.ManagedRealtimeEndpoint {
		t.Helper()
		ep, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx,
			state.ManagedRealtimeEndpoint{ID: uuid.NewString(), AccountID: account, AppID: app.ID, Enabled: true}, 10, 10)
		if err != nil {
			t.Fatal(err)
		}
		return ep
	}
	ep, other := endpoint(), endpoint()
	definition := apidserver.ExecutionRequest()
	definition.Input = nil
	definition.Source = "export default async () => ({reports: 1})"
	pub := api.PublishExecutionTaskRequest{Name: "report", Version: 1, Definition: definition, AgentEndpointIDs: []string{ep.ID, other.ID}}
	if r := e.Do(t, "POST", "/v1/run-tasks", pub, nil); r.Code != 201 {
		t.Fatal("publish", r.Code, r.Body.String())
	}
	server := httptest.NewServer(s.Handler())
	t.Cleanup(server.Close)
	client := api.NewClient(server.URL, *e.Key())
	data := []byte(strings.Repeat("safe qualification data\n", 8192))
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	file, err := client.PrepareExecutionFile(ctx, api.CreateExecutionFileRequest{SizeBytes: int64(len(data)), SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	const thread, callID = "report-thread", "assistant:uploaded-1"
	request := api.CreateAgentExecutionFileGrantRequest{EndpointID: ep.ID, ThreadID: thread, ToolCallID: callID,
		TaskName: "report", Version: 1, Path: "private/orders.json"}
	path := "/v1/execution-files/" + file.ID + "/agent-grants"
	if r := e.Do(t, "POST", path, request, nil); r.Code != 404 {
		t.Fatal("unpublished upload was delegated", r.Code)
	}
	if _, err := client.UploadExecutionFile(ctx, file.ID, bytes.NewReader(data), int64(len(data)), digest); err != nil {
		t.Fatal(err)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, account, hash, "runs-only", []string{api.ScopeRunsWrite}); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, "POST", path, request, map[string]string{"Authorization": "Bearer " + token}); r.Code != 403 {
		t.Fatal("Runs-only key managed delegation", r.Code)
	}
	grant, err := client.CreateAgentExecutionFileGrant(ctx, file.ID, request)
	if err != nil || grant.GrantToken == "" || !grant.ExpiresAt.After(time.Now()) {
		t.Fatal("delegate uploaded file", err)
	}
	callback := func(ep, thread string) string {
		t.Helper()
		r := e.Do(t, "POST", "/v1/agent-callbacks", api.CreateAgentCallbackRequest{EndpointID: ep, ThreadID: thread,
			Kinds: []string{api.AgentCallbackKindToolResult}}, nil)
		var cb api.AgentCallbackResponse
		if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &cb) != nil {
			t.Fatal("create callback", r.Code)
		}
		return cb.URL
	}
	invoke := func(thread, call, cb string, args any, want int) {
		t.Helper()
		r := e.Do(t, "POST", "/v1/agent-tools/invoke", map[string]any{"operation": "gregale_task", "id": call,
			"group_id": thread, "callback_url": cb, "arguments": args}, nil)
		if r.Code != want || strings.Contains(r.Body.String(), grant.GrantToken) {
			t.Fatalf("dispatch %s returned %d (want %d)", call, r.Code, want)
		}
	}
	args := map[string]any{"name": "report", "version": 1, "file_grants": []string{grant.GrantToken}}
	cb := callback(ep.ID, thread)
	invoke(thread, callID, callback(other.ID, thread), args, 200)
	invoke("other-thread", callID, callback(ep.ID, "other-thread"), args, 200)
	invoke(thread, "other-call", cb, args, 200)
	invoke(thread, "raw-file", cb, map[string]any{"name": "report", "version": 1,
		"input_files": []api.ExecutionFileInput{{FileID: file.ID, Path: request.Path}}}, 200)
	runs, err := store.ListExecutions(ctx, account, 10, 0)
	if err != nil || len(runs) != 0 {
		t.Fatal("out-of-scope dispatch admitted a Run", err, len(runs))
	}
	invoke(thread, callID, cb, args, 200)
	if err := client.DeleteExecutionFile(ctx, file.ID); err != nil {
		t.Fatal(err)
	}
	s.ConfigureAgentsForTest()
	s.SetAgentCallbackRevocationsForTest(revocations)
	invoke(thread, callID, cb, args, 200)
	change := map[string]any{"name": "report", "version": 1, "input": "changed", "file_grants": []string{grant.GrantToken}}
	invoke(thread, callID, cb, change, 422)
	history, err := store.ReadManagedRealtimeChannelHistory(ctx, ep.ID, api.AgentInboxChannel, 0, 10)
	if err != nil || len(history.Messages) != 3 {
		t.Fatal("an admitted replay or fingerprint conflict wrote a tool result", err, len(history.Messages))
	}
	for _, msg := range history.Messages {
		if bytes.Contains(msg.Data, []byte(grant.GrantToken)) || bytes.Contains(msg.Data, []byte(file.ID)) {
			t.Fatal("rejected callback exposed private file references")
		}
	}
	runs, err = store.ListExecutions(ctx, account, 10, 0)
	if err != nil || len(runs) != 1 {
		t.Fatal("replay did not recover exactly one Run", err, len(runs))
	}
	claim, err := store.ClaimExecutionForAccount(ctx, account, "schedd", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := executionpayload.DecodeRequest(ctx, []*age.X25519Identity{identity}, claim.SealedPayload, claim.PayloadKID)
	if err != nil || len(payload.StoredInputs) != 1 || payload.StoredInputs[0].Path != request.Path || payload.StoredInputs[0].SHA256 != digest ||
		bytes.Contains(payload.Input, []byte(grant.GrantToken)) {
		t.Fatal("sealed Run did not retain the pinned input descriptor", err)
	}
}
