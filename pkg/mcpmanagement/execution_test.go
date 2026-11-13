package mcpmanagement

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jobresult"
)

const executionRunID = "8bf4308d-8209-4f5c-885a-63e4264b3d51"
const executionRequestID = "6ed060ec-16b7-40d7-8a88-f10571d097a9"

type executionFixture struct {
	t         *testing.T
	mu        sync.Mutex
	job       api.JobResponse
	run       api.JobRunResponse
	manifest  json.RawMessage
	download  api.JobArtifactDownloadResponse
	requests  int
	starts    int
	cancels   int
	keys      map[string]string
	loseStart bool
	jobReads  int
	replaceAt int
	badTask   bool
	badPage   bool
	logs      string
	logBound  int
}

func newExecutionFixture(t *testing.T) (*executionFixture, *httptest.Server) {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	manifest, err := json.Marshal(jobresult.Manifest{Version: 1, Artifacts: []jobresult.Artifact{
		{Name: "report.csv", URI: "obj://private-app/private-bucket/private-key", SizeBytes: 9007199254740993, SHA256: digest},
		{Name: "external.txt", URI: "s3://private-external/key", SizeBytes: 1, SHA256: digest},
	}})
	if err != nil {
		t.Fatal(err)
	}
	f := &executionFixture{t: t, keys: map[string]string{}, manifest: manifest,
		job: api.JobResponse{ID: "job-one", AccountID: "account-one", Name: "report", Kind: "batch", Status: "active",
			ImageMaterializationStatus: "ready", ImageResolvedDigest: digest, ImageStorageKey: "private-layer",
			Command: []string{"private-command"}, EnvOverrides: map[string]string{"SECRET": "private-secret"}, RAMMB: 256, TaskTimeoutSec: 60, MaxParallelism: 1},
		run: api.JobRunResponse{ID: executionRunID, JobID: "job-one", AccountID: "account-one", Tasks: 1,
			AggregateStatus: "queued", ImageResolvedDigestSnapshot: digest, RAMMBSnapshot: 256, TaskTimeoutSec: 60,
			Command: []string{"private-command"}, EffectiveEnvSnapshot: map[string]string{"KEY": "private-secret"},
			EnvOverrides: map[string]string{"KEY": "private-secret"}, InputManifestURI: "obj://private-input/key"},
		download: api.JobArtifactDownloadResponse{Name: "report.csv", SizeBytes: 9007199254740993, SHA256: digest,
			VerifiedAt: time.Now().UTC().Format(time.RFC3339Nano), Download: api.ObjectSignedRequest{
				URL: "https://objects.example/download?signature=temporary", Method: "GET", Headers: map[string]string{}, ExpiresAt: time.Now().Add(time.Minute)}},
		logs: "application output " + testToken, logBound: executionLogBytes,
	}
	s := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(s.Close)
	return f, s
}

func (f *executionFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		f.t.Error("incorrect execution credential")
	}
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			f.t.Error(err)
		}
	}
	base := "/v1/jobs/report/runs/" + executionRunID
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/jobs":
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		next := -1
		if f.badPage {
			next = offset + 7
		}
		write(api.ListJobsResponse{Jobs: []api.JobResponse{f.job}, Limit: limit, Offset: offset, NextOffset: next, Total: 1})
	case r.Method == "GET" && r.URL.Path == "/v1/jobs/report":
		f.jobReads++
		if f.replaceAt > 0 && f.jobReads == f.replaceAt {
			f.job.ID = "replacement-job"
		}
		write(f.job)
	case r.Method == "POST" && r.URL.Path == "/v1/jobs/report/runs":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Error(err)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if key != "mcp-job-start-"+executionRequestID {
			f.t.Error("dispatch did not retain the caller's request identity")
		}
		if old, exists := f.keys[key]; exists && old != string(body) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			write(api.Problem{Status: http.StatusUnprocessableEntity, Code: api.CodeIdempotencyKeyReused, Detail: "request changed"})
			return
		}
		if _, exists := f.keys[key]; !exists {
			f.starts++
			f.keys[key] = string(body)
		}
		if f.loseStart {
			f.loseStart = false
			_, _ = w.Write([]byte("lost response"))
			return
		}
		write(f.run)
	case r.Method == "GET" && r.URL.Path == "/v1/jobs/report/runs":
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		write(api.ListJobRunsResponse{Runs: []api.JobRunResponse{f.run}, Limit: limit, Offset: offset, NextOffset: -1, Total: 1})
	case r.Method == "GET" && r.URL.Path == base:
		write(f.run)
	case r.Method == "POST" && r.URL.Path == base+"/cancel":
		f.cancels++
		f.run.AggregateStatus = "cancelled"
		write(api.JobRunCancelledResponse{Run: f.run, CancelledAt: time.Now().UTC().Format(time.RFC3339Nano)})
	case r.Method == "GET" && r.URL.Path == base+"/tasks":
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		task := api.JobTaskResponse{RunID: executionRunID, TaskIndex: offset, Status: "succeeded", Attempt: 1,
			InputRef: "private-input", InstanceID: "private-instance", ErrorMessage: "private-diagnostic", OutputManifest: f.manifest}
		if f.badTask {
			task.RunID = "another-run"
		}
		write(api.ListJobTasksResponse{Tasks: []api.JobTaskResponse{task}, Limit: limit, Offset: offset, NextOffset: -1, Total: 1})
	case r.Method == "GET" && r.URL.Path == base+"/tasks/0/logs":
		if r.URL.Query().Get("max_bytes") != strconv.Itoa(f.logBound) {
			f.t.Error("log bound not forwarded")
		}
		write(api.JobTaskLogResponse{TaskStatus: "succeeded", LogContent: f.logs, MaxBytes: f.logBound, Truncated: true})
	case r.Method == "GET" && r.URL.Path == base+"/tasks/0/artifacts/report.csv/download":
		write(f.download)
	default:
		f.t.Errorf("unexpected execution route %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}
}

func executionCall(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args any, wantError bool) map[string]json.RawMessage {
	t.Helper()
	out := callTool(t, ctx, cs, name, args, wantError)
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	validateWorkflowSchema(t, toolOutputSchema(name), data)
	for _, private := range []string{"private-command", "private-secret", "private-input", "private-instance", "private-layer", "private-app", "private-bucket", "private-key", "private-external"} {
		if strings.Contains(string(data), private) {
			t.Fatalf("%s exposed private job fields", name)
		}
	}
	return out
}

func TestExecutionJourney(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, backend := newExecutionFixture(t)
			opts := Options{APIBase: backend.URL, Token: testToken, Toolsets: []string{ToolsetExecute}, AllowWrites: true}
			cs, ctx := connectSelectedTestServer(t, backend, opts, protocol)
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) != 10 {
				t.Fatalf("catalog=%+v error=%v", catalog, err)
			}
			f.mu.Lock()
			requests := f.requests
			f.mu.Unlock()
			if requests != 0 {
				t.Fatal("execution discovery made API calls")
			}
			for _, tool := range catalog.Tools {
				write := tool.Name == "gregale_start_job" || tool.Name == "gregale_cancel_job_run"
				if tool.Annotations.ReadOnlyHint == write || !tool.Annotations.IdempotentHint {
					t.Fatalf("incorrect annotations: %s", tool.Name)
				}
			}
			page := executionCall(t, ctx, cs, "gregale_list_jobs", map[string]any{"limit": 1}, false)
			if string(page["next_offset"]) != "-1" {
				t.Fatal("lost native end-of-page marker")
			}
			executionCall(t, ctx, cs, "gregale_get_job", jobArgs{Job: "report"}, false)
			dispatch := startJobArgs{Job: "report", RequestID: executionRequestID}
			started := executionCall(t, ctx, cs, "gregale_start_job", dispatch, false)
			if string(started["run_id"]) != `"`+executionRunID+`"` || string(started["pending"]) != "true" || !strings.Contains(string(started["next"]), "after_seconds") {
				t.Fatal("dispatch omitted durable polling selectors")
			}
			executionCall(t, ctx, cs, "gregale_start_job", dispatch, false)
			executionCall(t, ctx, cs, "gregale_list_job_runs", jobRunsArgs{Job: "report", Limit: 1}, false)
			runArgs := jobRunArgs{Job: "report", RunID: executionRunID}
			executionCall(t, ctx, cs, "gregale_get_job_run", runArgs, false)
			tasks := executionCall(t, ctx, cs, "gregale_list_job_tasks", jobTasksArgs{Job: "report", RunID: executionRunID, Limit: 1}, false)
			if !strings.Contains(string(tasks["items"]), `"exit_code":0`) {
				t.Fatal("successful zero exit lost")
			}
			logs := executionCall(t, ctx, cs, "gregale_get_job_logs", jobLogsArgs{Job: "report", RunID: executionRunID}, false)
			if strings.Contains(string(logs["log_content"]), testToken) || string(logs["truncated"]) != "true" {
				t.Fatal("logs lost redaction or truncation")
			}
			artifacts := executionCall(t, ctx, cs, "gregale_list_artifacts", jobTaskArgs{Job: "report", RunID: executionRunID}, false)
			if !strings.Contains(string(artifacts["items"]), "9007199254740993") || string(artifacts["availability_verified"]) != "false" {
				t.Fatal("artifact size rounded or availability invented")
			}
			executionCall(t, ctx, cs, "gregale_read_artifact", jobArtifactArgs{Job: "report", RunID: executionRunID, Name: "external.txt"}, true)
			artifact := executionCall(t, ctx, cs, "gregale_read_artifact", jobArtifactArgs{Job: "report", RunID: executionRunID, Name: "report.csv"}, false)
			if string(artifact["size_bytes"]) != "9007199254740993" || !strings.Contains(string(artifact["download"]), "signature=temporary") {
				t.Fatal("download checksum metadata or signed capability changed")
			}
			cancelled := executionCall(t, ctx, cs, "gregale_cancel_job_run", runArgs, false)
			if string(cancelled["cancellation_requested"]) != "true" {
				t.Fatal("cancellation receipt missing")
			}
			executionCall(t, ctx, cs, "gregale_cancel_job_run", runArgs, false)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.starts != 1 || f.cancels != 2 {
				t.Fatalf("dispatches=%d cancels=%d", f.starts, f.cancels)
			}
		})
	}
}

func TestExecutionDispatchRecoveryAcrossConnections(t *testing.T) {
	f, backend := newExecutionFixture(t)
	f.loseStart = true
	opts := Options{APIBase: backend.URL, Toolsets: []string{ToolsetExecute}, AllowWrites: true}
	cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
	args := startJobArgs{Job: "report", RequestID: executionRequestID}
	out := executionCall(t, ctx, cs, "gregale_start_job", args, true)
	if string(out["outcome"]) != `"outcome_unknown"` || !strings.Contains(string(out["recovery"]), "retry_same_request") || !strings.Contains(string(out["recovery"]), "reuse_original_inputs") {
		t.Fatal("lost response advised unsafe recovery")
	}
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, restartedCtx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
	executionCall(t, restartedCtx, restarted, "gregale_start_job", args, false)
	args.Tasks = 2
	out = executionCall(t, restartedCtx, restarted, "gregale_start_job", args, true)
	if string(out["code"]) != `"idempotency_key_reused"` {
		t.Fatal("changed request did not preserve native conflict")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.starts != 1 {
		t.Fatalf("recovery created %d native runs", f.starts)
	}
}

func TestExecutionConsentAndReadOnlyBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  Options
		count int
	}{
		{"local_read_only", Options{}, 8},
		{"hosted_oauth", Options{AllowWrites: true, Environments: &EnvironmentConsent{DefaultWrite: true}}, 0},
		{"hosted_agent_key", Options{AllowWrites: true, agentKey: true}, 0},
		{"app_bound", Options{AllowWrites: true, AllowedApps: map[string]string{"alpha": "app-one"}}, 0},
		{"organization", Options{AllowWrites: true, Org: "acme"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, backend := newExecutionFixture(t)
			tc.opts.APIBase, tc.opts.Toolsets = backend.URL, []string{ToolsetExecute}
			cs, ctx := connectSelectedTestServer(t, backend, tc.opts, "2026-07-28")
			page, err := cs.ListTools(ctx, nil)
			if err != nil || len(page.Tools) != tc.count {
				t.Fatalf("catalog=%+v error=%v", page, err)
			}
			for _, tool := range []string{"gregale_start_job", "gregale_cancel_job_run"} {
				result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: map[string]any{}})
				if err == nil && !result.IsError {
					t.Fatalf("unlisted mutation %s was callable", tool)
				}
			}
			m := &management{opts: tc.opts}
			if tc.count == 0 && m.executionAccess() == nil {
				t.Fatal("handler-level consent guard permitted job access")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.requests != 0 {
				t.Fatal("denied tools reached the native API")
			}
		})
	}
}

func TestExecutionInvalidSelectorsDoNotReachAPI(t *testing.T) {
	f, backend := newExecutionFixture(t)
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetExecute}, AllowWrites: true}, "2026-07-28")
	for _, tc := range []struct {
		tool string
		args any
	}{
		{"gregale_get_job", jobArgs{Job: "../other"}},
		{"gregale_get_job_run", jobRunArgs{Job: "report", RunID: "x?other=true"}},
		{"gregale_start_job", startJobArgs{Job: "report", RequestID: ""}},
		{"gregale_start_job", startJobArgs{Job: "report", RequestID: executionRequestID, Tasks: -1}},
		{"gregale_list_jobs", map[string]any{"limit": 101}},
		{"gregale_list_job_runs", jobRunsArgs{Job: "report", Offset: -1}},
		{"gregale_list_artifacts", jobTaskArgs{Job: "report", RunID: executionRunID, TaskIndex: -1}},
		{"gregale_read_artifact", jobArtifactArgs{Job: "report", RunID: executionRunID, Name: ".."}},
		{"gregale_read_artifact", jobArtifactArgs{Job: "report", RunID: executionRunID, Name: "a/b"}},
		{"gregale_get_job_logs", jobLogsArgs{Job: "report", RunID: executionRunID, MaxBytes: executionLogBytes + 1}},
	} {
		executionCall(t, ctx, cs, tc.tool, tc.args, true)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.requests != 0 {
		t.Fatal("invalid selectors reached native API")
	}
}

func TestExecutionRejectsCommandAndEnvironmentOverrides(t *testing.T) {
	f, backend := newExecutionFixture(t)
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetExecute}, AllowWrites: true}, "2026-07-28")
	for _, field := range []string{"command", "arguments", "env_overrides"} {
		args := map[string]any{"job": "report", "request_id": executionRequestID, field: map[string]string{"SECRET": "private-secret"}}
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_start_job", Arguments: args})
		if err == nil && !result.IsError {
			t.Fatalf("dispatch accepted %s", field)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.requests != 0 {
		t.Fatal("unsupported overrides reached native API")
	}
}

func TestExecutionCancellationAndUnavailableOutputs(t *testing.T) {
	f, backend := newExecutionFixture(t)
	f.run.AggregateStatus, f.run.TasksRunning = "cancelled", 1
	f.manifest = nil
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetExecute}}, "2026-07-28")
	out := executionCall(t, ctx, cs, "gregale_get_job_run", jobRunArgs{Job: "report", RunID: executionRunID}, false)
	if string(out["pending"]) != "true" || out["next"] == nil {
		t.Fatal("cancelled aggregate hid running tasks")
	}
	out = executionCall(t, ctx, cs, "gregale_list_artifacts", jobTaskArgs{Job: "report", RunID: executionRunID}, false)
	if string(out["manifest_available"]) != "false" || string(out["items"]) != "[]" {
		t.Fatal("missing manifest was reported as available")
	}
}

func TestExecutionRejectsInconsistentNativeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, tool string
		mutate     func(*executionFixture)
		args       any
	}{
		{"cross_job_run", "gregale_get_job_run", func(f *executionFixture) { f.run.JobID = "another-job" }, jobRunArgs{Job: "report", RunID: executionRunID}},
		{"cross_account_run", "gregale_get_job_run", func(f *executionFixture) { f.run.AccountID = "another-account" }, jobRunArgs{Job: "report", RunID: executionRunID}},
		{"recreated_job", "gregale_get_job_run", func(f *executionFixture) { f.replaceAt = 2 }, jobRunArgs{Job: "report", RunID: executionRunID}},
		{"bad_page", "gregale_list_jobs", func(f *executionFixture) { f.badPage = true }, map[string]any{"limit": 1}},
		{"foreign_task", "gregale_list_artifacts", func(f *executionFixture) { f.badTask = true }, jobTaskArgs{Job: "report", RunID: executionRunID}},
		{"invalid_manifest", "gregale_list_artifacts", func(f *executionFixture) { f.manifest = json.RawMessage(`{"version":2}`) }, jobTaskArgs{Job: "report", RunID: executionRunID}},
		{"wrong_digest", "gregale_read_artifact", func(f *executionFixture) { f.download.SHA256 = "sha256:" + strings.Repeat("b", 64) }, jobArtifactArgs{Job: "report", RunID: executionRunID, Name: "report.csv"}},
		{"non_get_download", "gregale_read_artifact", func(f *executionFixture) { f.download.Download.Method = "PUT" }, jobArtifactArgs{Job: "report", RunID: executionRunID, Name: "report.csv"}},
		{"expired_download", "gregale_read_artifact", func(f *executionFixture) { f.download.Download.ExpiresAt = time.Now().Add(-time.Minute) }, jobArtifactArgs{Job: "report", RunID: executionRunID, Name: "report.csv"}},
		{"credential_header", "gregale_read_artifact", func(f *executionFixture) {
			f.download.Download.Headers = map[string]string{"Authorization": "Bearer private-secret"}
		}, jobArtifactArgs{Job: "report", RunID: executionRunID, Name: "report.csv"}},
		{"oversized_logs", "gregale_get_job_logs", func(f *executionFixture) { f.logs = strings.Repeat("x", executionLogBytes+1) }, jobLogsArgs{Job: "report", RunID: executionRunID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, backend := newExecutionFixture(t)
			tc.mutate(f)
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetExecute}}, "2026-07-28")
			executionCall(t, ctx, cs, tc.tool, tc.args, true)
		})
	}
}
