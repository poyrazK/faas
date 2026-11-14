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

const jobWaitRunID = "11111111-1111-4111-8111-111111111111"
const jobWaitJobID = "22222222-2222-4222-8222-222222222222"
const jobWaitRequestID = "0123456789abcdef0123456789abcdef"

type jobWaitDiagnostic struct {
	api.Problem
	Category  string `json:"category"`
	ExitCode  int    `json:"exit_code"`
	RequestID string `json:"request_id"`
	Recovery  *struct {
		Job            string `json:"job"`
		RunID          string `json:"run_id"`
		InspectCommand string `json:"inspect_command"`
		ResumeCommand  string `json:"resume_command"`
	} `json:"recovery"`
}

type jobWaitTestAPI struct {
	t                                *testing.T
	mode, token                      string
	headers                          []string
	accepted                         *api.JobRunResponse
	posts, jobs, polls, lists, other atomic.Int32
	polled, canceled                 chan struct{}
	polledOnce, canceledOnce         sync.Once
}

func newJobWaitTestAPI(t *testing.T, mode string) (*jobWaitTestAPI, *httptest.Server) {
	t.Helper()
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	main.ResetJSONOut(t)
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Setenv("FAAS_JSON", "0")
	f := &jobWaitTestAPI{t: t, mode: mode, token: "fp_test_job-wait-private", headers: []string{jobWaitRequestID}, polled: make(chan struct{}), canceled: make(chan struct{})}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", f.token)
	return f, server
}

func jobWaitTestRun(status string) api.JobRunResponse {
	return api.JobRunResponse{ID: jobWaitRunID, JobID: jobWaitJobID, AccountID: "job-wait-account", AggregateStatus: status, Tasks: 1}
}

func (f *jobWaitTestAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Connection", "close")
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		f.t.Error("Job wait changed the selected authentication")
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /v1/jobs/nightly":
		f.jobs.Add(1)
		_ = json.NewEncoder(w).Encode(api.JobResponse{Name: "nightly", ID: jobWaitJobID, AccountID: "job-wait-account"})
	case "POST /v1/jobs/nightly/runs":
		f.posts.Add(1)
		run := jobWaitTestRun("queued")
		if f.accepted != nil {
			run = *f.accepted
		}
		_ = json.NewEncoder(w).Encode(run)
	case "GET /v1/jobs/nightly/runs":
		f.lists.Add(1)
		_ = json.NewEncoder(w).Encode(api.ListJobRunsResponse{Runs: []api.JobRunResponse{jobWaitTestRun("queued")}})
	case "GET /v1/jobs/nightly/runs/" + jobWaitRunID:
		f.poll(w, r)
	default:
		f.other.Add(1)
		f.t.Errorf("wait submitted unexpected work or changed targets: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func (f *jobWaitTestAPI) poll(w http.ResponseWriter, r *http.Request) {
	n := f.polls.Add(1)
	f.polledOnce.Do(func() { close(f.polled) })
	switch f.mode {
	case "block":
		<-r.Context().Done()
		f.canceledOnce.Do(func() { close(f.canceled) })
		return
	case "transport":
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			f.t.Error(err)
			return
		}
		_ = conn.Close()
		return
	case "malformed":
		_, _ = io.WriteString(w, `{"id":`)
		return
	case "mismatched":
		run := jobWaitTestRun("succeeded")
		run.ID = "33333333-3333-4333-8333-333333333333"
		_ = json.NewEncoder(w).Encode(run)
		return
	}
	if f.mode == "unauthorized" || f.mode == "unavailable" || f.mode == "snapshot" && n > 1 {
		for _, id := range f.headers {
			w.Header().Add("X-Faas-Request-Id", id)
		}
		status, code := http.StatusServiceUnavailable, "service_unavailable"
		if f.mode == "unauthorized" {
			status, code = http.StatusUnauthorized, "unauthorized"
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(api.Problem{Status: status, Code: code, Title: "Poll failed", Detail: "private detail " + f.token, Hint: "server remediation " + f.token})
		return
	}
	status := f.mode
	if status == "timeout" || status == "snapshot" {
		status = "queued"
	}
	_ = json.NewEncoder(w).Encode(jobWaitTestRun(status))
}

func jobWaitTestArgs(submitted bool, ref, timeout, interval string) []string {
	args := []string{"--profile", "staging", "--json", "job"}
	if submitted {
		return append(args, "run", "nightly", "--tasks", "1", "--wait", "--wait-timeout", timeout, "--poll-interval", interval)
	}
	return append(args, "wait", "nightly", ref, "--timeout", timeout, "--poll-interval", interval)
}

func decodeSingleJobWaitJSON(t *testing.T, data string, value any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(data))
	if err := decoder.Decode(value); err != nil {
		t.Fatalf("not one JSON document: %v: %s", err, data)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("extra output after JSON: %v: %s", err, data)
	}
}

func assertJobWaitRecovery(t *testing.T, stderr string, code int) jobWaitDiagnostic {
	t.Helper()
	var problem jobWaitDiagnostic
	decodeSingleJobWaitJSON(t, stderr, &problem)
	if problem.ExitCode != code || problem.Category == "" || problem.Recovery == nil || problem.Recovery.Job != "nightly" || problem.Recovery.RunID != jobWaitRunID ||
		problem.Recovery.InspectCommand != "gregale --profile staging job status nightly "+jobWaitRunID ||
		!strings.HasPrefix(problem.Recovery.ResumeCommand, "gregale --profile staging job wait nightly "+jobWaitRunID+" --timeout ") || strings.Contains(problem.Recovery.ResumeCommand, "latest") {
		t.Fatalf("missing exact-run recovery: %s", stderr)
	}
	return problem
}

func TestJobWaitRecoveryPollingFailures(t *testing.T) {
	for _, submitted := range []bool{false, true} {
		for _, tc := range []struct{ mode, code string }{
			{"unauthorized", "unauthorized"}, {"unavailable", "service_unavailable"},
			{"transport", "transport_error"}, {"malformed", "invalid_request"}, {"mismatched", "invalid_request"},
		} {
			t.Run(tc.mode+"/submitted="+map[bool]string{false: "false", true: "true"}[submitted], func(t *testing.T) {
				f, _ := newJobWaitTestAPI(t, tc.mode)
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				code := main.Run(jobWaitTestArgs(submitted, jobWaitRunID, "1m", "1s"))
				if code != 1 || f.posts.Load() != map[bool]int32{false: 0, true: 1}[submitted] || f.polls.Load() != 1 || f.other.Load() != 0 {
					t.Fatalf("exit=%d posts=%d polls=%d stdout=%s stderr=%s", code, f.posts.Load(), f.polls.Load(), out, stderr())
				}
				problem := assertJobWaitRecovery(t, stderr(), code)
				wantStatus := http.StatusBadRequest
				if tc.mode == "unauthorized" {
					wantStatus = http.StatusUnauthorized
				} else if tc.mode == "unavailable" || tc.mode == "transport" {
					wantStatus = http.StatusServiceUnavailable
				}
				if problem.Code != tc.code || problem.Status != wantStatus || strings.Contains(out.String()+stderr(), f.token) || strings.Contains(out.String()+stderr(), "33333333-3333-4333-8333-333333333333") {
					t.Fatalf("lost cause or leaked untrusted data: stdout=%s stderr=%s", out, stderr())
				}
				var receipt map[string]json.RawMessage
				decodeSingleJobWaitJSON(t, out.String(), &receipt)
				wantFields := 3
				if submitted {
					wantFields = 4
					var run api.JobRunResponse
					if json.Unmarshal(receipt["run"], &run) != nil || run.ID != jobWaitRunID {
						t.Fatal("accepted run fallback was lost")
					}
				}
				if len(receipt) != wantFields || receipt["exit_code"] == nil || receipt["resume_command"] == nil || receipt["error"] == nil {
					t.Fatalf("legacy receipt shape changed: %s", out)
				}
				if tc.mode == "unauthorized" || tc.mode == "unavailable" {
					if problem.RequestID != jobWaitRequestID || !strings.Contains(problem.Hint, "server remediation [REDACTED]") {
						t.Fatalf("server remediation/correlation lost: %s", stderr())
					}
				} else if problem.RequestID != "" {
					t.Fatal("local failure invented a request ID")
				}
			})
		}
	}
}

func TestJobWaitRecoveryRequestTimeoutIsPollingFailure(t *testing.T) {
	for _, submitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "submitted"}[submitted], func(t *testing.T) {
			f, _ := newJobWaitTestAPI(t, "block")
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			args := append([]string{"--http-timeout", "250ms"}, jobWaitTestArgs(submitted, jobWaitRunID, "1m", "1s")...)
			if code := main.Run(args); code != 1 {
				t.Fatalf("request timeout changed wait exit: %d stdout=%s stderr=%s", code, out, stderr())
			}
			problem := assertJobWaitRecovery(t, stderr(), 1)
			if problem.Code != "transport_error" || f.polls.Load() != 1 || f.posts.Load() != map[bool]int32{false: 0, true: 1}[submitted] || f.other.Load() != 0 {
				t.Fatalf("request deadline became an overall wait timeout: %s", stderr())
			}
		})
	}
}

func TestJobWaitRecoveryTimeoutSnapshotAndTerminalOutcomes(t *testing.T) {
	for _, submitted := range []bool{false, true} {
		for _, mode := range []string{"timeout", "snapshot", "succeeded", "failed", "cancelled", "dead_letter"} {
			t.Run(mode+"/submitted="+map[bool]string{false: "false", true: "true"}[submitted], func(t *testing.T) {
				f, _ := newJobWaitTestAPI(t, mode)
				budget, interval, wantExit := "3s", "1s", 1
				if mode == "timeout" {
					budget, interval, wantExit = "250ms", "1h", 124
				} else if mode == "succeeded" {
					wantExit = 0
				}
				out, stderr, restore := main.SwapIO(t)
				defer restore()
				code := main.Run(jobWaitTestArgs(submitted, jobWaitRunID, budget, interval))
				var receipt struct {
					Run      *api.JobRunResponse `json:"run"`
					ExitCode int                 `json:"exit_code"`
					Error    string              `json:"error"`
				}
				decodeSingleJobWaitJSON(t, out.String(), &receipt)
				if code != wantExit || receipt.ExitCode != code || receipt.Run == nil || receipt.Run.ID != jobWaitRunID || f.other.Load() != 0 || f.posts.Load() != map[bool]int32{false: 0, true: 1}[submitted] {
					t.Fatalf("outcome changed: exit=%d stdout=%s stderr=%s", code, out, stderr())
				}
				if mode == "timeout" || mode == "snapshot" {
					problem := assertJobWaitRecovery(t, stderr(), code)
					if mode == "timeout" && problem.Code != "job_wait_timeout" || receipt.Run.AggregateStatus != "queued" || receipt.Error == "" {
						t.Fatalf("last snapshot or timeout cause lost: stdout=%s stderr=%s", out, stderr())
					}
				} else if stderr() != "" || receipt.Error != "" || receipt.Run.AggregateStatus != mode {
					t.Fatalf("terminal run became a wait error: stdout=%s stderr=%s", out, stderr())
				}
			})
		}
	}
}

func TestJobWaitRecoveryLatestAndCorrelationOmission(t *testing.T) {
	for _, headers := range [][]string{{}, {"bad-id"}, {jobWaitRequestID, jobWaitRequestID}, {jobWaitRequestID}} {
		t.Run(strings.Join(headers, ","), func(t *testing.T) {
			f, _ := newJobWaitTestAPI(t, "unavailable")
			f.headers = headers
			if len(headers) == 1 && headers[0] == jobWaitRequestID {
				f.token = jobWaitRequestID // a syntactically valid credential-matching ID
				t.Setenv("FAAS_TOKEN", f.token)
			}
			_, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.Run(jobWaitTestArgs(false, "latest", "1m", "1s")); code != 1 {
				t.Fatalf("exit=%d stderr=%s", code, stderr())
			}
			problem := assertJobWaitRecovery(t, stderr(), 1)
			if f.lists.Load() != 1 || f.polls.Load() != 1 || problem.RequestID != "" || strings.Contains(stderr(), f.token) {
				t.Fatalf("latest was not pinned or correlation was unsafe: %s", stderr())
			}
		})
	}
}

func TestJobWaitRecoveryDoesNotInventAcceptedIdentity(t *testing.T) {
	for _, mode := range []string{"missing-id", "invalid-id", "missing-job", "missing-account"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newJobWaitTestAPI(t, "queued")
			run := jobWaitTestRun("queued")
			switch mode {
			case "missing-id":
				run.ID = ""
			case "invalid-id":
				run.ID = "invalid/id"
			case "missing-job":
				run.JobID = ""
			case "missing-account":
				run.AccountID = ""
			}
			f.accepted = &run
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run(jobWaitTestArgs(true, "", "1m", "1s"))
			var problem jobWaitDiagnostic
			decodeSingleJobWaitJSON(t, stderr(), &problem)
			var preserved api.JobRunResponse
			decodeSingleJobWaitJSON(t, out.String(), &preserved)
			if code != 1 || f.posts.Load() != 1 || f.polls.Load() != 0 || problem.Recovery != nil || preserved.ID != run.ID {
				t.Fatalf("unverified acceptance was followed: exit=%d stdout=%s stderr=%s", code, out, stderr())
			}
		})
	}
}

func TestJobWaitRecoverySignalProcess(t *testing.T) {
	if os.Getenv("GREGALE_JOB_WAIT_HELPER") != "1" {
		t.Skip("signal process helper")
	}
	_ = os.Setenv("FAAS_API", os.Getenv("GREGALE_JOB_WAIT_API"))
	_ = os.Setenv("FAAS_TOKEN", os.Getenv("GREGALE_JOB_WAIT_TOKEN"))
	_ = os.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("GREGALE_JOB_WAIT_ARGS")), &args); err != nil {
		t.Fatal(err)
	}
	os.Exit(main.Run(args))
}

func TestJobWaitRecoveryInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process interrupt delivery requires POSIX signals")
	}
	for _, submitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "submitted"}[submitted], func(t *testing.T) {
			f, server := newJobWaitTestAPI(t, "block")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			args, err := json.Marshal(jobWaitTestArgs(submitted, jobWaitRunID, "1m", "1s"))
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJobWaitRecoverySignalProcess$")
			cmd.Env = append(os.Environ(), "GREGALE_JOB_WAIT_HELPER=1", "GREGALE_JOB_WAIT_API="+server.URL, "GREGALE_JOB_WAIT_TOKEN="+f.token, "GREGALE_JOB_WAIT_ARGS="+string(args))
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
				t.Fatal("child never polled the target run")
			}
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			var exited *exec.ExitError
			if err := <-done; !errors.As(err, &exited) || exited.ExitCode() != 130 {
				t.Fatalf("interrupt changed exit: %v stdout=%s stderr=%s", err, &out, &stderr)
			}
			problem := assertJobWaitRecovery(t, stderr.String(), 130)
			if problem.Code != "job_wait_interrupted" || f.posts.Load() != map[bool]int32{false: 0, true: 1}[submitted] || f.polls.Load() != 1 || f.other.Load() != 0 {
				t.Fatalf("interrupt changed durable work: stdout=%s stderr=%s", &out, &stderr)
			}
			select {
			case <-f.canceled:
			case <-ctx.Done():
				t.Fatal("interrupt did not close the active read")
			}
		})
	}
}
