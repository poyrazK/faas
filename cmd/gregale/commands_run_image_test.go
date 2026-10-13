package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1161
func TestRunImageRequestedOnlyBeforeTerminator(t *testing.T) {
	cases := map[string]struct {
		args []string
		want bool
	}{
		"flag":            {[]string{"--image", "alpine", "true"}, true},
		"equals form":     {[]string{"--image=alpine"}, true},
		"source mode":     {[]string{"--runtime", "node24", "--source", "x"}, false},
		"after separator": {[]string{"--runtime", "node24", "--", "--image"}, false},
	}
	for name, tc := range cases {
		if got := runImageRequested(tc.args); got != tc.want {
			t.Errorf("%s: runImageRequested(%q) = %v, want %v", name, tc.args, got, tc.want)
		}
	}
}

// adr: 1161
func TestParseImageRunArgsFollowsDockerSemantics(t *testing.T) {
	strs := func(v ...string) *[]string { return &v }
	cases := map[string]struct {
		args       []string
		entrypoint *[]string
		cmd        *[]string
		env        map[string]string
		timeoutSec int
	}{
		"image CMD":                {args: []string{"--image", "alpine"}},
		"args stop flag parsing":   {args: []string{"--image", "alpine", "-e", "A=1", "sh", "-c", "echo -n hi"}, cmd: strs("sh", "-c", "echo -n hi"), env: map[string]string{"A": "1"}},
		"entrypoint and separator": {args: []string{"--image", "alpine", "--entrypoint", "/bin/sh", "--timeout", "90s", "--", "-c", "true"}, entrypoint: strs("/bin/sh"), cmd: strs("-c", "true"), timeoutSec: 90},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			opts, ok := parseImageRunArgs(tc.args)
			req := opts.request
			if !ok || req.Image != "alpine" || !reflect.DeepEqual(req.Entrypoint, tc.entrypoint) ||
				!reflect.DeepEqual(req.Args, tc.cmd) || !reflect.DeepEqual(req.Env, tc.env) || req.TimeoutSec != tc.timeoutSec {
				t.Fatalf("parse(%q) = %+v, %v", tc.args, req, ok)
			}
		})
	}
	_, _, restore := swapIO(t)
	defer restore()
	for _, bad := range [][]string{{"alpine"}, {"--image", "alpine", "-e", "NOEQUALS"}, {"--image", "alpine", "--timeout", "1500ms"}} {
		if _, ok := parseImageRunArgs(bad); ok {
			t.Errorf("parse(%q) accepted", bad)
		}
	}
}

// adr: 1161
func TestCmdRunImagePullsThenPrintsOutputAndExitCode(t *testing.T) {
	var mu sync.Mutex
	var pulls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/image-runs":
			var req api.CreateImageRunRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			pulls = append(pulls, req.Pull)
			first := len(pulls) == 1
			mu.Unlock()
			if first {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"status":"pulling","job":"ir-0123","image":"docker.io/library/alpine:latest","image_status":"pending"}`))
				return
			}
			if req.Args == nil || strings.Join(*req.Args, " ") != "sh -c exit 3" {
				t.Errorf("args = %v", req.Args)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"status":"queued","job":"ir-0123","image":"docker.io/library/alpine:latest","image_status":"ready","run_id":"run-1","command":["sh","-c","exit 3"]}`))
		case "GET /v1/jobs/ir-0123":
			_, _ = w.Write([]byte(`{"id":"job-1","name":"ir-0123","kind":"image_run","image_materialization_status":"ready"}`))
		case "GET /v1/jobs/ir-0123/runs/run-1":
			_, _ = w.Write([]byte(`{"id":"run-1","job_id":"job-1","aggregate_status":"dead_letter"}`))
		case "GET /v1/jobs/ir-0123/runs/run-1/tasks":
			_, _ = w.Write([]byte(`{"tasks":[{"run_id":"run-1","task_index":0,"status":"dead_letter","exit_code":3}]}`))
		case "GET /v1/jobs/ir-0123/runs/run-1/tasks/0/logs":
			_, _ = w.Write([]byte(`{"task_status":"dead_letter","log_content":"hello from alpine\n","truncated":false,"max_bytes":1048576}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, _, restore := swapIO(t)
	defer restore()
	if got := cmdRunImage([]string{"--image", "alpine", "--pull", "always", "sh", "-c", "exit 3"}); got != 3 {
		t.Fatalf("exit = %d, want the task's exit code 3", got)
	}
	if out.String() != "hello from alpine\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if !reflect.DeepEqual(pulls, []string{"always", "missing"}) {
		t.Fatalf("pull policies = %q; only the first request may re-queue the image", pulls)
	}
}

// adr: 1161
func TestImageRunExitCodeMapsOutcomes(t *testing.T) {
	cases := map[string]struct {
		task api.JobTaskResponse
		want int
	}{
		"success":         {api.JobTaskResponse{Status: "succeeded"}, 0},
		"command failure": {api.JobTaskResponse{Status: "dead_letter", ExitCode: 42}, 42},
		"deadline":        {api.JobTaskResponse{Status: "timeout"}, 124},
		"cancelled":       {api.JobTaskResponse{Status: "cancelled"}, 130},
		"boot failure":    {api.JobTaskResponse{Status: "failed"}, 1},
	}
	for name, tc := range cases {
		if got := imageRunExitCode(tc.task); got != tc.want {
			t.Errorf("%s: exit = %d, want %d", name, got, tc.want)
		}
	}
}
