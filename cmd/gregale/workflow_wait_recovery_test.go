package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const workflowWaitID = "11111111-1111-4111-8111-111111111111"
const workflowWaitAppID = "22222222-2222-4222-8222-222222222222"
const workflowWaitRequestID = "0123456789abcdef0123456789abcdef"

type workflowWaitDiagnostic struct {
	api.Problem
	ExitCode  int    `json:"exit_code"`
	Category  string `json:"category"`
	RequestID string `json:"request_id"`
	Recovery  *struct {
		RunID          string `json:"run_id"`
		InspectCommand string `json:"inspect_command"`
		ResumeCommand  string `json:"resume_command"`
	} `json:"recovery"`
}

type workflowWaitAPI struct {
	t                              *testing.T
	mode, token                    string
	status                         int
	headers                        []string
	invalidAcceptance              bool
	starts, resumes, retries, gets atomic.Int32
	lists, other                   atomic.Int32
	polled, canceled               chan struct{}
	polledOnce, canceledOnce       sync.Once
}

func newWorkflowWaitAPI(t *testing.T, mode string) (*workflowWaitAPI, *httptest.Server) {
	t.Helper()
	main.SetupConnectionProfiles(t)
	main.ResetJSONOut(t)
	t.Chdir(t.TempDir())
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Setenv("FAAS_JSON", "0")
	f := &workflowWaitAPI{t: t, mode: mode, token: "fp_test_workflow-wait-private", status: 401, headers: []string{workflowWaitRequestID},
		polled: make(chan struct{}), canceled: make(chan struct{})}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", f.token)
	return f, server
}

func workflowWaitRun(status string) api.WorkflowRunResponse {
	return api.WorkflowRunResponse{ID: workflowWaitID, AppID: workflowWaitAppID, WorkflowName: "orders", Status: status}
}

func (f *workflowWaitAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Connection", "close")
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		f.t.Error("workflow wait changed credentials")
	}
	if r.Method == http.MethodGet && r.Header.Get("Idempotency-Key") != "" {
		f.t.Error("workflow polling inherited a mutation's key")
	}
	accepted := workflowWaitRun("running")
	if f.invalidAcceptance {
		accepted.ID = "not-a-run-id"
	}
	switch r.Method + " " + r.URL.Path {
	case "POST /v1/apps/api/workflows/orders/runs":
		f.starts.Add(1)
		if r.Header.Get("Idempotency-Key") != "workflow-start-private-key" {
			f.t.Error("original submission lost its key")
		}
		_ = json.NewEncoder(w).Encode(accepted)
	case "POST /v1/workflows/runs/" + workflowWaitID + "/resume":
		f.resumes.Add(1)
		var request api.ResumeWorkflowRunRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedResumeCount == nil || *request.ExpectedResumeCount != 0 || r.Header.Get("Idempotency-Key") != "workflow-resume-private-key" {
			f.t.Error("original resume changed its count or key")
		}
		_ = json.NewEncoder(w).Encode(accepted)
	case "POST /v1/workflows/runs/" + workflowWaitID + "/steps/charge/retry":
		f.retries.Add(1)
		if r.Header.Get("Idempotency-Key") == "" {
			f.t.Error("original retry lost its generated key")
		}
		_ = json.NewEncoder(w).Encode(accepted)
	case "GET /v1/workflows/runs/" + workflowWaitID:
		f.poll(w, r)
	case "GET /v1/apps/api/workflows/runs":
		f.lists.Add(1)
		if r.URL.Query().Get("workflow_name") != "orders" || r.URL.Query().Get("limit") != "1" {
			f.t.Error("latest lookup changed scope")
		}
		_ = json.NewEncoder(w).Encode(api.ListWorkflowRunsResponse{Runs: []api.WorkflowRunResponse{workflowWaitRun("running")}, Total: 1})
	default:
		f.other.Add(1)
		f.t.Errorf("wait mutated work or changed target: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func (f *workflowWaitAPI) poll(w http.ResponseWriter, r *http.Request) {
	n := f.gets.Add(1)
	if f.mode == "block" {
		f.polledOnce.Do(func() { close(f.polled) })
		<-r.Context().Done()
		f.canceledOnce.Do(func() { close(f.canceled) })
		return
	}
	if f.mode == "permanent" || f.mode == "snapshot" && n > 1 || f.mode == "persistent" || f.mode == "transient" && n == 1 {
		for _, id := range f.headers {
			w.Header().Add("X-Faas-Request-Id", id)
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(f.status)
		_ = json.NewEncoder(w).Encode(api.Problem{Status: f.status, Code: "workflow_read_refused", Title: "Read refused",
			Detail: "private server detail " + f.token, Hint: "server remediation " + f.token})
		return
	}
	if f.mode == "transport" && n == 1 {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			f.t.Error(err)
			return
		}
		_ = conn.Close()
		return
	}
	if f.mode == "malformed" && n == 1 {
		_, _ = io.WriteString(w, `{"id":`)
		return
	}
	run := workflowWaitRun("succeeded")
	switch f.mode {
	case "pending", "snapshot":
		run.Status = "running"
	case "mismatch":
		run.ID = "33333333-3333-4333-8333-333333333333"
	case "failed", "dead", "unknown":
		run.Status = f.mode
	case "cancelled":
		at := "2026-11-14T00:00:00Z"
		run.CancelledAt = &at
	}
	_ = json.NewEncoder(w).Encode(run)
}

func workflowWaitArgs(action, timeout, interval string) []string {
	args := []string{"--profile", "staging", "--json", "workflow"}
	switch action {
	case "run":
		args = append(args, "run", "orders", "--app", "api", "--input", `{"private_payload":true}`, "--idempotency-key", "workflow-start-private-key", "--wait")
	case "resume":
		args = append(args, "resume", workflowWaitID, "--expected-resume-count", "0", "--idempotency-key", "workflow-resume-private-key", "--wait")
	case "retry":
		args = append(args, "retry", workflowWaitID, "charge", "--wait")
	case "latest":
		args = append(args, "wait", "latest", "--app", "api", "--workflow-name", "orders")
	default:
		args = append(args, "wait", workflowWaitID)
	}
	return append(args, "--timeout", timeout, "--poll-interval", interval)
}

func decodeWorkflowWaitJSON(t *testing.T, text string, result any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(text))
	if err := decoder.Decode(result); err != nil {
		t.Fatalf("not one JSON document: %v: %s", err, text)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("extra output after JSON: %v: %s", err, text)
	}
}

func assertWorkflowWaitRecovery(t *testing.T, stderr string, code int, timeout, interval string) workflowWaitDiagnostic {
	t.Helper()
	var problem workflowWaitDiagnostic
	decodeWorkflowWaitJSON(t, stderr, &problem)
	duration, _ := time.ParseDuration(timeout)
	poll, _ := time.ParseDuration(interval)
	if problem.ExitCode != code || problem.Category == "" || problem.Recovery == nil || problem.Recovery.RunID != workflowWaitID ||
		problem.Recovery.InspectCommand != "gregale --profile staging workflow status "+workflowWaitID ||
		problem.Recovery.ResumeCommand != "gregale --profile staging workflow wait "+workflowWaitID+" --timeout "+duration.String()+" --poll-interval "+poll.String() ||
		strings.Contains(stderr, "fp_test_workflow-wait-private") || strings.Contains(stderr, "private_payload") || strings.Contains(stderr, "private-key") {
		t.Fatalf("lost safe exact-run recovery: %s", stderr)
	}
	return problem
}

func (f *workflowWaitAPI) assertMutations(t *testing.T, action string) {
	t.Helper()
	if f.starts.Load() != map[bool]int32{false: 0, true: 1}[action == "run"] || f.resumes.Load() != map[bool]int32{false: 0, true: 1}[action == "resume"] ||
		f.retries.Load() != map[bool]int32{false: 0, true: 1}[action == "retry"] || f.other.Load() != 0 {
		t.Fatalf("wait repeated a mutation: starts=%d resumes=%d retries=%d other=%d", f.starts.Load(), f.resumes.Load(), f.retries.Load(), f.other.Load())
	}
}

func TestWorkflowWaitRecoveryPermanentFailures(t *testing.T) {
	for _, action := range []string{"wait", "run", "resume", "retry"} {
		for _, tc := range []struct{ status, exit int }{{401, 2}, {403, 6}, {404, 4}, {409, 5}} {
			t.Run(action+http.StatusText(tc.status), func(t *testing.T) {
				f, _ := newWorkflowWaitAPI(t, "permanent")
				f.status = tc.status
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				code := main.Run(workflowWaitArgs(action, "1m", "1s"))
				problem := assertWorkflowWaitRecovery(t, stderr(), tc.exit, "1m", "1s")
				if code != tc.exit || problem.Status != tc.status || problem.Code != "workflow_read_refused" || problem.RequestID != workflowWaitRequestID || !strings.Contains(problem.Hint, "server remediation") || f.gets.Load() != 1 {
					t.Fatalf("API failure lost its contract: exit=%d stdout=%s stderr=%s", code, out, stderr())
				}
				if action == "wait" {
					if out.Len() != 0 {
						t.Fatalf("invented a snapshot: %s", out)
					}
				} else {
					var run api.WorkflowRunResponse
					decodeWorkflowWaitJSON(t, out.String(), &run)
					if run.ID != workflowWaitID || run.Status != "running" {
						t.Fatalf("lost acceptance snapshot: %s", out)
					}
				}
				f.assertMutations(t, action)
			})
		}
	}
}

func TestWorkflowWaitRecoverySnapshotAndIdentity(t *testing.T) {
	for _, mode := range []string{"snapshot", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newWorkflowWaitAPI(t, mode)
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			want := 2
			if mode == "mismatch" {
				want = 1
			}
			code := main.Run(workflowWaitArgs("wait", "1m", "1s"))
			assertWorkflowWaitRecovery(t, stderr(), want, "1m", "1s")
			if code != want || mode == "mismatch" && out.Len() != 0 {
				t.Fatalf("identity mismatch changed output: exit=%d stdout=%s stderr=%s", code, out, stderr())
			}
			if mode == "snapshot" {
				var run api.WorkflowRunResponse
				decodeWorkflowWaitJSON(t, out.String(), &run)
				if run.ID != workflowWaitID || run.Status != "running" || f.gets.Load() != 2 {
					t.Fatalf("lost last verified snapshot: %s", out)
				}
			}
			f.assertMutations(t, "wait")
		})
	}
}

func TestWorkflowWaitRecoveryTimeoutAndLatest(t *testing.T) {
	for _, action := range []string{"wait", "latest", "run", "resume", "retry"} {
		t.Run(action, func(t *testing.T) {
			f, _ := newWorkflowWaitAPI(t, "pending")
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run(workflowWaitArgs(action, "250ms", "30s"))
			problem := assertWorkflowWaitRecovery(t, stderr(), 124, "250ms", "30s")
			var run api.WorkflowRunResponse
			decodeWorkflowWaitJSON(t, out.String(), &run)
			if code != 124 || problem.Code != "workflow_wait_timeout" || problem.RequestID != "" || run.ID != workflowWaitID || run.Status != "running" ||
				f.lists.Load() != map[bool]int32{false: 0, true: 1}[action == "latest"] {
				t.Fatalf("timeout lost target or deadline: exit=%d stdout=%s stderr=%s", code, out, stderr())
			}
			f.assertMutations(t, action)
		})
	}
}

func TestWorkflowWaitRecoveryRetainsPollingPolicy(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		status int
	}{{"transient", 503}, {"transient", 408}, {"transient", 429}, {"transport", 0}, {"malformed", 0}, {"persistent", 503}} {
		t.Run(tc.mode+http.StatusText(tc.status), func(t *testing.T) {
			f, _ := newWorkflowWaitAPI(t, tc.mode)
			f.status = tc.status
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			timeout := "3s"
			if tc.mode == "persistent" {
				timeout = "2500ms"
			}
			code := main.Run(workflowWaitArgs("wait", timeout, "1s"))
			if tc.mode == "persistent" {
				problem := assertWorkflowWaitRecovery(t, stderr(), 124, timeout, "1s")
				if code != 124 || problem.Code != "workflow_wait_timeout" || problem.RequestID != "" || out.Len() != 0 {
					t.Fatalf("transient failure replaced overall timeout: exit=%d stdout=%s stderr=%s", code, out, stderr())
				}
			} else {
				var run api.WorkflowRunResponse
				decodeWorkflowWaitJSON(t, out.String(), &run)
				if code != 0 || stderr() != "" || run.ID != workflowWaitID || run.Status != "succeeded" {
					t.Fatalf("wait stopped retrying a transient read: exit=%d stdout=%s stderr=%s", code, out, stderr())
				}
			}
			if tc.mode == "persistent" && f.gets.Load() < 2 || tc.mode != "persistent" && f.gets.Load() != 2 {
				t.Fatalf("poll policy changed: %d reads", f.gets.Load())
			}
			f.assertMutations(t, "wait")
		})
	}
}

func TestWorkflowWaitRecoveryTerminalOutcomes(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "dead", "cancelled", "unknown"} {
		t.Run(status, func(t *testing.T) {
			f, _ := newWorkflowWaitAPI(t, status)
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run(workflowWaitArgs("wait", "1m", "1s"))
			var run api.WorkflowRunResponse
			decodeWorkflowWaitJSON(t, out.String(), &run)
			if run.ID != workflowWaitID || f.gets.Load() != 1 || status == "succeeded" && (code != 0 || stderr() != "") {
				t.Fatalf("terminal receipt changed: exit=%d stdout=%s stderr=%s", code, out, stderr())
			}
			if status != "succeeded" {
				problem := assertWorkflowWaitRecovery(t, stderr(), 1, "1m", "1s")
				if code != 1 || problem.Code != "invalid_request" {
					t.Fatalf("terminal exit changed: %d %s", code, stderr())
				}
			}
			f.assertMutations(t, "wait")
		})
	}
}

func TestWorkflowWaitRecoveryUnverifiedAcceptance(t *testing.T) {
	for _, action := range []string{"run", "resume", "retry"} {
		t.Run(action, func(t *testing.T) {
			f, _ := newWorkflowWaitAPI(t, "pending")
			f.invalidAcceptance = true
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run(workflowWaitArgs(action, "1m", "1s"))
			var problem workflowWaitDiagnostic
			decodeWorkflowWaitJSON(t, stderr(), &problem)
			var raw api.WorkflowRunResponse
			decodeWorkflowWaitJSON(t, out.String(), &raw)
			if code != 1 || problem.Recovery != nil || raw.ID != "not-a-run-id" || f.gets.Load() != 0 {
				t.Fatalf("unverified acceptance was followed or corrupted: exit=%d stdout=%s stderr=%s", code, out, stderr())
			}
			f.assertMutations(t, action)
		})
	}
}

func TestWorkflowWaitRecoveryCorrelationOmission(t *testing.T) {
	for _, mode := range []string{"missing", "malformed", "duplicate", "credential"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newWorkflowWaitAPI(t, "permanent")
			switch mode {
			case "missing":
				f.headers = nil
			case "malformed":
				f.headers = []string{"not-correlation"}
			case "duplicate":
				f.headers = []string{workflowWaitRequestID, workflowWaitRequestID}
			case "credential":
				f.token = workflowWaitRequestID
				t.Setenv("FAAS_TOKEN", f.token)
			}
			_, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run(workflowWaitArgs("wait", "1m", "1s"))
			problem := assertWorkflowWaitRecovery(t, stderr(), 2, "1m", "1s")
			var fields map[string]json.RawMessage
			decodeWorkflowWaitJSON(t, stderr(), &fields)
			if code != 2 || problem.RequestID != "" || fields["request_id"] != nil || strings.Contains(stderr(), f.token) {
				t.Fatalf("unsafe correlation was retained: %s", stderr())
			}
		})
	}
}

func TestWorkflowWaitRecoveryHumanTiming(t *testing.T) {
	_, _ = newWorkflowWaitAPI(t, "pending")
	args := workflowWaitArgs("wait", "250ms", "30s")
	args = append(args[:2], args[3:]...)
	_, stderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run(args); code != 124 || !strings.Contains(stderr(), "workflow wait "+workflowWaitID+" --timeout 250ms --poll-interval 30s") || json.Valid([]byte(stderr())) {
		t.Fatalf("human recovery lost polling interval: exit=%d stderr=%s", code, stderr())
	}
}

func TestWorkflowWaitRecoverySignalProcess(t *testing.T) {
	if os.Getenv("GREGALE_WORKFLOW_WAIT_HELPER") != "1" {
		t.Skip("signal process helper")
	}
	main.SetupConnectionProfiles(t)
	_ = os.Setenv("FAAS_API", os.Getenv("GREGALE_WORKFLOW_WAIT_API"))
	_ = os.Setenv("FAAS_TOKEN", os.Getenv("GREGALE_WORKFLOW_WAIT_TOKEN"))
	_ = os.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("GREGALE_WORKFLOW_WAIT_ARGS")), &args); err != nil {
		t.Fatal(err)
	}
	os.Exit(main.Run(args))
}

func TestWorkflowWaitRecoveryInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("real interrupt delivery requires POSIX signals")
	}
	for _, action := range []string{"wait", "run", "resume", "retry"} {
		t.Run(action, func(t *testing.T) {
			f, server := newWorkflowWaitAPI(t, "block")
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			args, _ := json.Marshal(workflowWaitArgs(action, "1m", "1s"))
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkflowWaitRecoverySignalProcess$")
			cmd.Env = append(os.Environ(), "GREGALE_WORKFLOW_WAIT_HELPER=1", "GREGALE_WORKFLOW_WAIT_API="+server.URL,
				"GREGALE_WORKFLOW_WAIT_TOKEN="+f.token, "GREGALE_WORKFLOW_WAIT_ARGS="+string(args))
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-f.polled:
			case err := <-done:
				t.Fatalf("child stopped before polling: %v stderr=%s", err, &stderr)
			case <-ctx.Done():
				t.Fatal("child never polled workflow")
			}
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			var exited *exec.ExitError
			if err := <-done; !errors.As(err, &exited) || exited.ExitCode() != 130 {
				t.Fatalf("interrupt changed exit: %v stdout=%s stderr=%s", err, &out, &stderr)
			}
			problem := assertWorkflowWaitRecovery(t, stderr.String(), 130, "1m", "1s")
			if problem.Code != "workflow_wait_interrupted" || f.gets.Load() != 1 {
				t.Fatalf("interrupt lost recovery: %s", &stderr)
			}
			f.assertMutations(t, action)
			select {
			case <-f.canceled:
			case <-ctx.Done():
				t.Fatal("interrupt did not close the active workflow read")
			}
		})
	}
}
