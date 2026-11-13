package journeys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const jobLogReconnectRun = "11111111-1111-4111-8111-111111111111"

func jobReconnectFrame(instance string, seq int, stream, line string) string {
	return fmt.Sprintf("event: log\ndata: {\"instance\":%q,\"seq\":%d,\"stream\":%q,\"line\":%q}\n\n", instance, seq, stream, line)
}

// GA row 156: the built CLI reads the public stream protocol. A dropped
// response must replay only unseen output, including when the VM changes.
func TestJobLogReconnectIsolatedAPI(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	first := jobReconnectFrame("vm-a", 1, "stdout", "one") + jobReconnectFrame("vm-a", 2, "stderr", "two") + jobReconnectFrame("vm-a", 3, "stdout", "three")
	end := "event: end\ndata: {\"reason\":\"task_finished\"}\n\n"
	for _, tc := range []struct {
		name, replay, output, warning string
	}{
		{"recovered", first + jobReconnectFrame("vm-a", 4, "stdout", "four") + end, "one\nthree\nfour\n", ""},
		{"expired printed prefix", "event: gap\ndata: {\"reason\":\"seq_below_retained\"}\n\n" + jobReconnectFrame("vm-a", 3, "stdout", "three") + jobReconnectFrame("vm-a", 4, "stdout", "four") + end, "one\nthree\nfour\n", ""},
		{"attempt changed", jobReconnectFrame("vm-b", 1, "stdout", "new attempt") + end, "one\nthree\nnew attempt\n", "attempt changed while reconnecting"},
		{"empty resumed attempt", end, "one\nthree\n", "did not identify its attempt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/jobs/batch/runs/"+jobLogReconnectRun+"/tasks/0/logs/stream" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer fp_job-log-private" {
					t.Error("follow changed its read target, attempt-safe replay, or authentication")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if calls.Add(1) == 1 {
					_, _ = io.WriteString(w, first)
				} else {
					_, _ = io.WriteString(w, tc.replay)
				}
			}))
			defer server.Close()
			tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
			res := tg.exec(t, "fp_job-log-private", tg.home, 20*time.Second, "--json=false", "job", "logs", "batch", jobLogReconnectRun, "0", "--follow")
			if res.exit != 0 || string(res.stdout) != tc.output || calls.Load() != 2 || strings.Count(string(res.stderr), "two\n") != 1 {
				t.Fatalf("follow exit=%d requests=%d stdout=%q stderr=%q", res.exit, calls.Load(), res.stdout, res.stderr)
			}
			if tc.warning == "" && bytes.Contains(res.stderr, []byte("incomplete")) || tc.warning != "" && !bytes.Contains(res.stderr, []byte(tc.warning)) {
				t.Fatalf("incorrect completeness warning: %s", res.stderr)
			}
		})
	}
}

func TestJobLogReconnectInterrupt(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	opened, closed := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) != 1 {
			t.Error("interrupted follow reopened its stream")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, jobReconnectFrame("vm-a", 1, "stdout", "one"))
		w.(http.Flusher).Flush()
		close(opened)
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tg.cli, "--non-interactive", "--json=false", "job", "logs", "batch", jobLogReconnectRun, "0", "--follow")
	cmd.Env, cmd.Dir = tg.cliEnv("fp_job-log-private"), tg.home
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("CLI did not open the log stream")
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 130 {
			t.Fatalf("interrupt returned %v, want exit 130", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("interrupt did not stop log following")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("interrupt left the response open")
	}
}

// The live smoke check is read-only and uses an existing successful task.
// Controlled disconnects and attempt changes belong to the isolated journey.
func TestJobLogFollowLiveAPI(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	if tg.token == "" {
		t.Skip("read-only Job discovery requires a configured bearer key")
	}
	read := func(path string, dest any) {
		t.Helper()
		res := tg.apiCall(http.MethodGet, path, nil, tg.token)
		if res.status == http.StatusNotFound || res.status == http.StatusPaymentRequired || res.status == http.StatusForbidden {
			t.Skip("Jobs are unavailable for the journey account")
		}
		if res.err != nil || res.status != http.StatusOK || json.Unmarshal(res.body, dest) != nil {
			t.Fatalf("read-only Job discovery failed: status=%d error=%v", res.status, res.err)
		}
	}
	var jobs struct {
		Jobs []struct{ Name string }
	}
	read("/v1/jobs?limit=1", &jobs)
	if len(jobs.Jobs) == 0 {
		t.Skip("no existing Job to inspect")
	}
	job := jobs.Jobs[0].Name
	if job == "" {
		t.Fatal("Job discovery omitted its name")
	}
	var runs struct {
		Runs []struct{ ID string }
	}
	read("/v1/jobs/"+url.PathEscape(job)+"/runs?limit=1", &runs)
	if len(runs.Runs) == 0 {
		t.Skip("existing Job has no run")
	}
	run := runs.Runs[0].ID
	if run == "" {
		t.Fatal("run discovery omitted its ID")
	}
	var tasks struct {
		Tasks []struct {
			Index  int `json:"task_index"`
			Status string
		}
	}
	read("/v1/jobs/"+url.PathEscape(job)+"/runs/"+url.PathEscape(run)+"/tasks?limit=20", &tasks)
	for _, task := range tasks.Tasks {
		if task.Status == "succeeded" {
			res := tg.run(t, 30*time.Second, "--json=false", "job", "logs", job, run, fmt.Sprint(task.Index), "--follow")
			if res.exit != 0 {
				t.Fatalf("following the completed task exited %d", res.exit)
			}
			return
		}
	}
	t.Skip("the sampled run has no successful retained task")
}
