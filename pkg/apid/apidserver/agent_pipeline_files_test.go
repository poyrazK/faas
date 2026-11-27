package apidserver_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestAgentPipelineFileInputs(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	testAgentPipelineFileInputs(t, *e.S(), *e.Store(), e.Acct().ID, *e.Key(), e.Do, nil)
}

func TestAgentPipelineFileInputsPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	testAgentPipelineFileInputs(t, *e.S(), *e.Store(), e.Acct().ID, *e.Key(), e.Do, *e.Pool())
}

func testAgentPipelineFileInputs(t *testing.T, s *apidserver.Server, st state.Store, account, key string,
	do func(*testing.T, string, string, any, map[string]string) *httptest.ResponseRecorder, pool *pgxpool.Pool,
) {
	t.Helper()
	identity, restore := apidserver.WithTestIdentities(t)
	t.Cleanup(restore)
	s.WithExecutionAPIEnabled(true).WithExecutionV2Enabled(true).WithExecutionTasksEnabled(true).WithExecutionPipelinesEnabled(true)
	s.ConfigureAgentsForTest()
	s.SetAgentCallbackRevocationsForTest(&memCallbackRevocations{at: map[string]time.Time{}})
	ctx := t.Context()
	pipelines := st.(state.ExecutionPipelineStore)
	executions := st.(state.ExecutionStore)
	endpoints := st.(state.ManagedRealtimeEndpointStore)
	progress := st.(state.ExecutionPipelineProgressStore)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	check(err)
	s.WithExecutionFileStorage(backend)
	app, err := st.CreateApp(ctx, state.App{AccountID: account, Slug: "scoped-pipeline", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, Status: state.AppActive})
	check(err)
	ep, err := endpoints.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		ID: uuid.NewString(), AccountID: account, AppID: app.ID, Enabled: true,
		CallbackURL: "https://example.test/inbox", ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect",
		CallbackAuthTokenSealed: []byte("fixture"), AuthTokenSealed: []byte("fixture"),
	}, 10, 10)
	check(err)
	definition := apidserver.ExecutionRequest()
	definition.Input = nil
	pub := api.PublishExecutionTaskRequest{Name: "report", Version: 1, Definition: definition, AgentEndpointIDs: []string{ep.ID}}
	if r := do(t, "POST", "/v1/run-tasks", pub, nil); r.Code != 201 {
		t.Fatal("publish", r.Code, r.Body.String())
	}

	server := httptest.NewServer(s.Handler())
	t.Cleanup(server.Close)
	client := api.NewClient(server.URL, key)
	data := []byte(strings.Repeat("ord-1,north,1,200\n", 1000))
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	file, err := client.PrepareExecutionFile(ctx, api.CreateExecutionFileRequest{SizeBytes: int64(len(data)), SHA256: digest})
	check(err)
	_, err = client.UploadExecutionFile(ctx, file.ID, bytes.NewReader(data), int64(len(data)), digest)
	check(err)
	const thread = "report-thread"
	var callback api.AgentCallbackResponse
	cb := do(t, "POST", "/v1/agent-callbacks", api.CreateAgentCallbackRequest{EndpointID: ep.ID, ThreadID: thread, Kinds: []string{api.AgentCallbackKindToolResult}}, nil)
	if cb.Code != 201 || json.Unmarshal(cb.Body.Bytes(), &callback) != nil {
		t.Fatal("callback", cb.Code)
	}
	grant := func(call, step string) string {
		t.Helper()
		r, err := client.CreateAgentExecutionFileGrant(ctx, file.ID, api.CreateAgentExecutionFileGrantRequest{
			EndpointID: ep.ID, ThreadID: thread, ToolCallID: call, TaskName: "report", Version: 1, StepLabel: step, Path: "orders.csv",
		})
		check(err)
		return r.GrantToken
	}
	invoke := func(call, step, token string, want int) {
		t.Helper()
		r := do(t, "POST", "/v1/agent-tools/invoke", map[string]any{"operation": "gregale_pipeline", "id": call,
			"group_id": thread, "callback_url": callback.URL, "arguments": map[string]any{"steps": []any{
				map[string]any{"label": "check", "task": map[string]any{"name": "report", "version": 1},
					"file_grants": grantsForStep(step, "check", token)},
				map[string]any{"label": "report", "depends_on": []string{"check"}, "task": map[string]any{"name": "report", "version": 1},
					"file_grants": grantsForStep(step, "report", token)},
			}}}, nil)
		if r.Code != want || strings.Contains(r.Body.String(), token) {
			t.Fatalf("dispatch %s wanted HTTP %d, got %d", call, want, r.Code)
		}
	}
	invoke("wrong-step", "check", grant("wrong-step", "report"), 200)
	invoke("task-grant", "report", grant("task-grant", ""), 200)
	if _, err = pipelines.ClaimExecutionPipeline(ctx, "unscoped", time.Now(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("incompatible grant admitted a workflow", err)
	}

	const call = "valid-report"
	token := grant(call, "report")
	invoke(call, "report", token, 200)
	invoke(call, "check", token, 409)
	if pool != nil {
		// Simulate the prior worker's progress claim without scoped input support.
		tx, err := pool.Begin(ctx)
		check(err)
		defer tx.Rollback(ctx) //nolint:errcheck
		_, err = tx.Exec(ctx, "SELECT set_config('faas.run_pipeline_progress_protocol','1',true)")
		check(err)
		result, err := tx.Exec(ctx, "UPDATE agent_execution_workflows SET status='running',lease_token=gen_random_uuid() WHERE account_id=$1 AND status='queued'", account)
		check(err)
		if result.RowsAffected() != 0 {
			t.Fatal("an older pipeline worker could claim a scoped-file plan")
		}
		check(tx.Rollback(ctx))
	}
	claim, err := pipelines.ClaimExecutionPipeline(ctx, "first", time.Now(), time.Minute)
	check(err)
	check(s.DriveExecutionPipelineForTest(ctx, claim))
	first, err := executions.ClaimExecution(ctx, "schedd", time.Now(), time.Minute)
	check(err)
	decode := func(run state.ExecutionClaim) executionpayload.DecodedPayload {
		t.Helper()
		payload, err := executionpayload.DecodeRequest(ctx, []*age.X25519Identity{identity}, run.SealedPayload, run.PayloadKID)
		check(err)
		return payload
	}
	if len(decode(first).StoredInputs) != 0 {
		t.Fatal("an earlier task received the report step's private upload")
	}
	_, err = executions.MarkExecutionRunning(ctx, first.ID, *first.LeaseToken, time.Now())
	check(err)
	_, err = executions.CompleteExecution(ctx, state.CompleteExecutionParams{ID: first.ID, LeaseToken: *first.LeaseToken,
		Status: api.ExecutionStatusSucceeded, Result: []byte(`{"ready":true}`), FinishedAt: time.Now()})
	check(err)

	s.ConfigureAgentsForTest()
	s.SetAgentCallbackRevocationsForTest(&memCallbackRevocations{at: map[string]time.Time{}})
	claim, err = pipelines.ClaimExecutionPipeline(ctx, "replacement", time.Now().Add(3*time.Second), time.Minute)
	check(err)
	check(s.DriveExecutionPipelineForTest(ctx, claim))
	second, err := executions.ClaimExecution(ctx, "schedd", time.Now(), time.Minute)
	check(err)
	payload := decode(second)
	if len(payload.StoredInputs) != 1 || payload.StoredInputs[0].Path != "orders.csv" || payload.StoredInputs[0].SHA256 != digest ||
		bytes.Contains(payload.Input, []byte(token)) {
		t.Fatal("the intended step did not receive only the scoped upload")
	}
	const revokedCall = "revoked-before-admission"
	revokedGrant := grant(revokedCall, "check")
	invoke(revokedCall, "check", revokedGrant, 200)
	check(client.DeleteExecutionFile(ctx, file.ID))
	invoke(call, "report", token, 200)
	revoked, err := pipelines.ClaimExecutionPipeline(ctx, "revoked-input", time.Now(), time.Minute)
	check(err)
	if revoked.ID == claim.ID {
		t.Fatal("claimed the wrong pipeline")
	}
	check(s.DriveExecutionPipelineForTest(ctx, revoked))
	blocked, err := pipelines.ExecutionPipelineByID(ctx, revoked.ID)
	check(err)
	if blocked.Completion == nil || blocked.Completion.Outcome != "failed" || blocked.Completion.Steps[0].Outcome != "blocked" {
		t.Fatal("a pending step with a revoked upload did not settle as blocked")
	}
	runs, err := executions.ListExecutions(ctx, account, 10, 0)
	check(err)
	if len(runs) != 2 {
		t.Fatal("replay created another Run", len(runs))
	}
	events, err := progress.ListExecutionPipelineEvents(ctx, account, claim.ID, nil, 0, 25)
	check(err)
	raw, err := json.Marshal(events)
	check(err)
	if bytes.Contains(raw, []byte(token)) || bytes.Contains(raw, []byte(file.ID)) {
		t.Fatal("public progress exposed private upload metadata")
	}
}

func grantsForStep(target, step, token string) []string {
	if target == step {
		return []string{token}
	}
	return nil
}
