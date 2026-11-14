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
	"syscall"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const healthRecoveryRequestID = "0123456789abcdef0123456789abcdef"

type healthWaitDiagnostic struct {
	api.Problem
	ExitCode  int    `json:"exit_code"`
	Category  string `json:"category"`
	RequestID string `json:"request_id"`
}

// Decode command fields separately so both JSON spelling and optional scope
// are part of the public-output assertions.
type healthWaitRecoveryFields struct {
	App            string `json:"app"`
	Environment    string `json:"environment"`
	InspectCommand string `json:"inspect_command"`
	ResumeCommand  string `json:"resume_command"`
}

type healthWaitAPI struct {
	t                     *testing.T
	mode, app, env, token string
	status                int
	headers               []string
	first                 api.AppHealthResponse
	reads, other          atomic.Int32
	polled, canceled      chan struct{}
	polledOnce, endOnce   sync.Once
}

func healthWaitAssessment(scope, status string) api.AppHealthResponse {
	return api.AppHealthResponse{AppID: "11111111-1111-4111-8111-111111111111", Scope: scope, Status: status,
		Phase: "serving", Summary: "Recorded health evidence", EvaluatedAt: time.Now().UTC().Format(time.RFC3339Nano), ValidForSeconds: 120,
		ServingDeploymentIDs: []string{"deployment-one"}}
}

func newHealthWaitAPI(t *testing.T, mode, environment string) (*healthWaitAPI, *httptest.Server) {
	t.Helper()
	main.SetupConnectionProfiles(t)
	main.ResetJSONOut(t)
	t.Chdir(t.TempDir())
	t.Setenv("FAAS_JSON", "0")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	scope := environment
	if scope == "" {
		scope = "default"
	}
	f := &healthWaitAPI{t: t, mode: mode, app: "demo", env: environment, token: "fp_test_health-recovery-private", status: 403,
		headers: []string{healthRecoveryRequestID}, first: healthWaitAssessment(scope, "degraded"), polled: make(chan struct{}), canceled: make(chan struct{})}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", f.token)
	return f, server
}

func (f *healthWaitAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet || r.Header.Get("Idempotency-Key") != "" {
		f.other.Add(1)
		f.t.Errorf("health recovery mutated state: %s %s", r.Method, r.URL.Path)
		http.Error(w, "read-only health required", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		f.t.Error("health wait changed credentials")
	}
	if r.URL.Path == "/v1/capabilities" {
		_, _ = w.Write([]byte(`{"hidden_capabilities":[]}`))
		return
	}
	if r.URL.Path != "/v1/apps/"+f.app+"/health" || r.URL.Query().Get("environment") != f.env || (f.env == "" && r.URL.RawQuery != "") {
		f.other.Add(1)
		f.t.Errorf("health wait changed target: %s", r.URL)
		http.NotFound(w, r)
		return
	}
	n := f.reads.Add(1)
	if f.mode == "block" || f.mode == "snapshot-block" && n > 1 {
		f.polledOnce.Do(func() { close(f.polled) })
		<-r.Context().Done()
		f.endOnce.Do(func() { close(f.canceled) })
		return
	}
	if f.mode == "error" || f.mode == "snapshot" && n > 1 || f.mode == "transient" && n == 1 {
		for _, id := range f.headers {
			w.Header().Add(api.RequestIDHeader, id)
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(f.status)
		_ = json.NewEncoder(w).Encode(api.Problem{Status: f.status, Code: "health_read_refused", Title: "Health read refused",
			Detail: "private detail " + f.token, Hint: "server remediation " + f.token})
		return
	}
	if f.mode == "malformed" && n == 1 {
		_, _ = w.Write([]byte(`not JSON`))
		return
	}
	if f.mode == "transport" && n == 1 {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			f.t.Error(err)
			return
		}
		_ = connection.Close()
		return
	}
	out := f.first
	if f.mode == "transient" || f.mode == "malformed" || f.mode == "transport" || f.mode == "freshness" && n > 1 || f.mode == "healthy" {
		out = healthWaitAssessment(out.Scope, "healthy")
		if f.mode == "freshness" {
			out.Scope = "staging"
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}

func healthWaitArgs(environment, timeout, interval string) []string {
	args := []string{"--profile", "staging", "--json", "app", "health", "--app", "demo", "--wait", "--timeout", timeout, "--poll-interval", interval}
	if environment != "" {
		args = append(args, "--env", environment)
	}
	return args
}

func healthWaitDecode(t *testing.T, value string, result any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(value))
	if err := decoder.Decode(result); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, value)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Fatalf("expected one JSON object: %v: %s", err, value)
	}
}

func assertHealthWaitRecovery(t *testing.T, value, environment, timeout, interval string, code int) healthWaitDiagnostic {
	t.Helper()
	var problem healthWaitDiagnostic
	healthWaitDecode(t, value, &problem)
	var fields struct {
		Recovery *healthWaitRecoveryFields `json:"recovery"`
	}
	healthWaitDecode(t, value, &fields)
	inspect := "gregale --profile staging app health --app demo"
	if environment != "" {
		inspect += " --env " + environment
	}
	if fields.Recovery == nil || fields.Recovery.App != "demo" || fields.Recovery.Environment != environment || fields.Recovery.InspectCommand != inspect ||
		fields.Recovery.ResumeCommand != inspect+" --wait --timeout "+timeout+" --poll-interval "+interval || problem.ExitCode != code || problem.Category == "" {
		t.Fatalf("lost health recovery contract: %s", value)
	}
	for _, command := range []string{fields.Recovery.InspectCommand, fields.Recovery.ResumeCommand} {
		// This hint validator takes runtime paths with app placeholders, not
		// global connection options. The journey executes the full literal.
		path := strings.Replace(command, "gregale --profile staging ", "gregale ", 1)
		path = strings.Replace(path, "--app demo", "--app <app>", 1)
		path = strings.Replace(path, "--env ", "--environment ", 1)
		if err := main.ResolveHintCommand(path); err != nil {
			t.Fatalf("recovery is not an existing command: %v", err)
		}
	}
	return problem
}

func TestAppHealthWaitRecoveryPermanentErrors(t *testing.T) {
	for _, status := range []struct{ http, exit int }{{401, 2}, {403, 6}, {404, 4}, {409, 5}} {
		for _, snapshot := range []bool{false, true} {
			t.Run(http.StatusText(status.http)+map[bool]string{true: "/snapshot", false: "/no-observation"}[snapshot], func(t *testing.T) {
				mode := "error"
				if snapshot {
					mode = "snapshot"
				}
				f, _ := newHealthWaitAPI(t, mode, "staging")
				f.status = status.http
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				code := main.Run(healthWaitArgs("staging", "10s", "1s"))
				problem := assertHealthWaitRecovery(t, stderr(), "staging", "10s", "1s", status.exit)
				if code != status.exit || problem.Status != status.http || problem.Code != "health_read_refused" || problem.RequestID != healthRecoveryRequestID ||
					!strings.Contains(problem.Hint, "server remediation") || strings.Contains(stderr(), f.token) || f.other.Load() != 0 {
					t.Fatalf("API cause/correlation/exit changed: exit=%d stderr=%s", code, stderr())
				}
				if !snapshot {
					if out.Len() != 0 || f.reads.Load() != 1 {
						t.Fatalf("invented an observation: %s", out)
					}
				} else {
					var got api.AppHealthResponse
					healthWaitDecode(t, out.String(), &got)
					if got.AppID != f.first.AppID || got.EvaluatedAt != f.first.EvaluatedAt || got.Status != "degraded" || got.Scope != "staging" || f.reads.Load() != 2 {
						t.Fatalf("lost the last assessment: %s", out)
					}
				}
			})
		}
	}
}

func TestAppHealthWaitRecoveryTimeoutScopeAndLink(t *testing.T) {
	for _, environment := range []string{"", "staging", "production"} {
		t.Run("scope="+environment, func(t *testing.T) {
			f, _ := newHealthWaitAPI(t, "pending", environment)
			root, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = main.SaveProjectContext(root, main.LocalProjectContext{Version: 2, App: "demo"}); err != nil {
				t.Fatal(err)
			}
			args := healthWaitArgs(environment, "200ms", "30s")
			// Let the existing app resolver supply the target from the link.
			args = append(args[:5], args[7:]...)
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.Run(args); code != 124 {
				t.Fatalf("timeout exit=%d stderr=%s", code, stderr())
			}
			problem := assertHealthWaitRecovery(t, stderr(), environment, "200ms", "30s", 124)
			var got api.AppHealthResponse
			healthWaitDecode(t, out.String(), &got)
			if problem.Status != 408 || problem.Code != "" || problem.RequestID != "" || got.EvaluatedAt != f.first.EvaluatedAt || got.Status != "degraded" || f.reads.Load() != 1 || f.other.Load() != 0 {
				t.Fatalf("timeout changed observation/Problem: stdout=%s stderr=%s", out, stderr())
			}
			if environment == "" && strings.Contains(stderr(), `"environment"`) {
				t.Fatalf("default scope became an explicit environment: %s", stderr())
			}
		})
	}
	for _, mode := range []string{"block", "error"} {
		t.Run("no-observation/"+mode, func(t *testing.T) {
			f, _ := newHealthWaitAPI(t, mode, "")
			f.status = 503
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.Run(healthWaitArgs("", "200ms", "30s")); code != 124 {
				t.Fatalf("timeout exit=%d: %s", code, stderr())
			}
			problem := assertHealthWaitRecovery(t, stderr(), "", "200ms", "30s", 124)
			if out.Len() != 0 || problem.RequestID != "" || problem.Code != "" {
				t.Fatalf("invented observation or reused transient correlation: stdout=%s stderr=%s", out, stderr())
			}
		})
	}
}

func TestAppHealthWaitRecoveryPollingAndFreshness(t *testing.T) {
	for _, mode := range []string{"healthy", "408", "429", "503", "transport", "malformed", "stale", "future", "wrong-scope", "no-validity"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newHealthWaitAPI(t, "transient", "staging")
			switch mode {
			case "408":
				f.status = 408
			case "429":
				f.status = 429
			case "503":
				f.status = 503
			case "healthy", "transport", "malformed":
				f.mode = mode
			default:
				f.mode = "freshness"
				f.first.Status = "healthy"
				switch mode {
				case "stale":
					f.first.EvaluatedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
				case "future":
					f.first.EvaluatedAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
				case "wrong-scope":
					f.first.Scope = "default"
				case "no-validity":
					f.first.ValidForSeconds = 0
				}
			}
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run(healthWaitArgs("staging", "10s", "1s"))
			var got api.AppHealthResponse
			healthWaitDecode(t, out.String(), &got)
			wantReads := int32(2)
			if mode == "healthy" {
				wantReads = 1
			}
			if code != 0 || stderr() != "" || f.reads.Load() != wantReads || f.other.Load() != 0 || got.Status != "healthy" || got.Scope != "staging" {
				t.Fatalf("polling/freshness changed: exit=%d reads=%d stdout=%s stderr=%s", code, f.reads.Load(), out, stderr())
			}
		})
	}
}

func TestAppHealthWaitRecoveryTargetValidation(t *testing.T) {
	for _, app := range []string{"pr-7-" + strings.Repeat("a", 40), "tag-legacy-app", "not an app slug"} {
		t.Run(app, func(t *testing.T) {
			f, _ := newHealthWaitAPI(t, "error", "")
			f.app, f.status = app, 404
			args := healthWaitArgs("", "10s", "1s")
			args[6] = app
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.Run(args); code != 4 || out.Len() != 0 || f.reads.Load() != 1 {
				t.Fatalf("target changed: exit=%d stdout=%s stderr=%s", code, out, stderr())
			}
			var got struct {
				Recovery *healthWaitRecoveryFields `json:"recovery"`
			}
			healthWaitDecode(t, stderr(), &got)
			if app != "not an app slug" {
				if got.Recovery == nil || got.Recovery.App != app || !strings.Contains(got.Recovery.InspectCommand, "app health --app "+app) {
					t.Fatalf("valid readable app lost recovery: %s", stderr())
				}
			} else if got.Recovery != nil {
				t.Fatalf("invented recovery for invalid target: %s", stderr())
			}
		})
	}
}

func TestAppHealthWaitRecoveryRejectsInvalidOptions(t *testing.T) {
	for _, options := range [][]string{{"--env", "default"}, {"--env", "Not-A-Slug"}, {"--timeout", "0"}, {"--poll-interval", "500ms"}} {
		t.Run(strings.Join(options, "="), func(t *testing.T) {
			f, _ := newHealthWaitAPI(t, "pending", "")
			args := append(healthWaitArgs("", "1s", "1s"), options...)
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run(args)
			var problem healthWaitDiagnostic
			healthWaitDecode(t, stderr(), &problem)
			if code != 1 || problem.ExitCode != 1 || out.Len() != 0 || f.reads.Load() != 0 || strings.Contains(stderr(), `"recovery"`) {
				t.Fatalf("invalid input reached health reads or invented recovery: exit=%d stdout=%s stderr=%s", code, out, stderr())
			}
		})
	}
}

type healthWaitBrokenOutput struct{}

func (healthWaitBrokenOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestAppHealthWaitRecoverySnapshotWriteFailure(t *testing.T) {
	f, _ := newHealthWaitAPI(t, "snapshot", "staging")
	_, stderr, restore := main.SwapIO(t)
	defer restore()
	*main.OsStdout = healthWaitBrokenOutput{}
	code := main.Run(healthWaitArgs("staging", "10s", "1s"))
	var problem healthWaitDiagnostic
	healthWaitDecode(t, stderr(), &problem)
	if code != 1 || problem.ExitCode != 1 || f.reads.Load() != 2 || strings.Contains(stderr(), "health_read_refused") || strings.Contains(stderr(), `"recovery"`) {
		t.Fatalf("snapshot write failure was overwritten: exit=%d stderr=%s", code, stderr())
	}
}

func TestAppHealthWaitRecoveryHumanOutput(t *testing.T) {
	for _, mode := range []string{"pending", "error"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newHealthWaitAPI(t, mode, "staging")
			args := healthWaitArgs("staging", "200ms", "30s")
			args = append(args[:2], args[3:]...)
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			want := 124
			if mode == "error" {
				want = 6
			}
			if code := main.Run(args); code != want || json.Valid([]byte(stderr())) || strings.Contains(stderr(), f.token) || strings.Contains(stderr(), `"recovery"`) {
				t.Fatalf("human error contract changed: exit=%d stdout=%s stderr=%s", code, out, stderr())
			}
		})
	}
}

func TestAppHealthWaitRecoveryCorrelationOmission(t *testing.T) {
	for _, mode := range []string{"missing", "malformed", "duplicate", "credential"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newHealthWaitAPI(t, "error", "staging")
			switch mode {
			case "missing":
				f.headers = nil
			case "malformed":
				f.headers = []string{"untrusted-correlation"}
			case "duplicate":
				f.headers = []string{healthRecoveryRequestID, healthRecoveryRequestID}
			case "credential":
				f.token = healthRecoveryRequestID
				t.Setenv("FAAS_TOKEN", f.token)
			}
			_, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.Run(healthWaitArgs("staging", "10s", "1s")); code != 6 {
				t.Fatalf("exit=%d stderr=%s", code, stderr())
			}
			problem := assertHealthWaitRecovery(t, stderr(), "staging", "10s", "1s", 6)
			if problem.RequestID != "" || strings.Contains(stderr(), `"request_id"`) || strings.Contains(stderr(), f.token) {
				t.Fatalf("untrusted correlation or credential retained: %s", stderr())
			}
		})
	}
}

func TestAppHealthWaitRecoveryRedactsTargetMetadata(t *testing.T) {
	f, _ := newHealthWaitAPI(t, "error", "staging")
	f.token, f.headers = "staging", nil
	t.Setenv("FAAS_TOKEN", f.token)
	_, stderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run(healthWaitArgs("staging", "10s", "1s")); code != 6 {
		t.Fatalf("exit=%d stderr=%s", code, stderr())
	}
	var got struct {
		Recovery *healthWaitRecoveryFields `json:"recovery"`
	}
	healthWaitDecode(t, stderr(), &got)
	if got.Recovery == nil || got.Recovery.App != "demo" || strings.Contains(stderr(), f.token) {
		t.Fatalf("credential leaked through profile/environment recovery: %s", stderr())
	}
}

func TestAppHealthWaitRecoverySignalProcess(t *testing.T) {
	if os.Getenv("GREGALE_HEALTH_WAIT_HELPER") != "1" {
		t.Skip("signal process helper")
	}
	main.SetupConnectionProfiles(t)
	_ = os.Setenv("FAAS_API", os.Getenv("GREGALE_HEALTH_WAIT_API"))
	_ = os.Setenv("FAAS_TOKEN", os.Getenv("GREGALE_HEALTH_WAIT_TOKEN"))
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("GREGALE_HEALTH_WAIT_ARGS")), &args); err != nil {
		t.Fatal(err)
	}
	os.Exit(main.Run(args))
}

func TestAppHealthWaitRecoveryInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("real interrupt delivery requires POSIX signals")
	}
	for _, snapshot := range []bool{false, true} {
		t.Run(map[bool]string{false: "SIGINT/no-observation", true: "SIGTERM/snapshot"}[snapshot], func(t *testing.T) {
			mode := "block"
			if snapshot {
				mode = "snapshot-block"
			}
			f, server := newHealthWaitAPI(t, mode, "staging")
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			args, _ := json.Marshal(healthWaitArgs("staging", "1m", "1s"))
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAppHealthWaitRecoverySignalProcess$")
			cmd.Env = append(os.Environ(), "GREGALE_HEALTH_WAIT_HELPER=1", "GREGALE_HEALTH_WAIT_API="+server.URL,
				"GREGALE_HEALTH_WAIT_TOKEN="+f.token, "GREGALE_HEALTH_WAIT_ARGS="+string(args))
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
				t.Fatal("child never polled health")
			}
			signal := os.Interrupt
			if snapshot {
				signal = syscall.SIGTERM
			}
			if err := cmd.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			var exited *exec.ExitError
			if err := <-done; !errors.As(err, &exited) || exited.ExitCode() != 130 {
				t.Fatalf("interrupt changed exit: %v stdout=%s stderr=%s", err, &out, &stderr)
			}
			problem := assertHealthWaitRecovery(t, stderr.String(), "staging", "1m0s", "1s", 130)
			if problem.Code != "transport_error" || problem.Status != 503 || problem.RequestID != "" || f.other.Load() != 0 {
				t.Fatalf("interrupt changed existing Problem: %s", &stderr)
			}
			if snapshot {
				var got api.AppHealthResponse
				healthWaitDecode(t, out.String(), &got)
				if got.EvaluatedAt != f.first.EvaluatedAt || got.Status != "degraded" || f.reads.Load() != 2 {
					t.Fatalf("interrupt lost last assessment: %s", &out)
				}
			} else if out.Len() != 0 || f.reads.Load() != 1 {
				t.Fatalf("interrupt invented an assessment: %s", &out)
			}
			select {
			case <-f.canceled:
			case <-ctx.Done():
				t.Fatal("interrupt did not cancel the active read")
			}
		})
	}
}
