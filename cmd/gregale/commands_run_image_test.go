package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/cmd/gregale"
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
		if got := main.RunImageRequested(tc.args); got != tc.want {
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
			opts, ok := main.ParseImageRunArgs(tc.args)
			req := (*opts.Request())
			if !ok || req.Image != "alpine" || !reflect.DeepEqual(req.Entrypoint, tc.entrypoint) ||
				!reflect.DeepEqual(req.Args, tc.cmd) || !reflect.DeepEqual(req.Env, tc.env) || req.TimeoutSec != tc.timeoutSec {
				t.Fatalf("parse(%q) = %+v, %v", tc.args, req, ok)
			}
		})
	}
	_, _, restore := main.SwapIO(t)
	defer restore()
	for _, bad := range [][]string{{"alpine"}, {"--image", "alpine", "-e", "NOEQUALS"}, {"--image", "alpine", "--timeout", "1500ms"}} {
		if _, ok := main.ParseImageRunArgs(bad); ok {
			t.Errorf("parse(%q) accepted", bad)
		}
	}
}

// imageRunServer fakes the API behind `gregale run --image`; stream writes
// the live-output response.
func imageRunServer(t *testing.T, pulls *[]string, stream func(http.ResponseWriter)) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/image-runs":
			var req api.CreateImageRunRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			*pulls = append(*pulls, req.Pull)
			first := len(*pulls) == 1
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
		case "GET /v1/jobs/ir-0123/runs/run-1/tasks/0/logs/stream":
			stream(w)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func sseLog(seq int, stream, line string) string {
	return fmt.Sprintf("event: log\ndata: {\"seq\":%d,\"instance\":\"inst-1\",\"stream\":%q,\"line\":%q,\"written_at\":\"2026-10-14T00:00:00Z\"}\n\n", seq, stream, line)
}

// adr: 1161
// adr: 1163
func TestCmdRunImagePullsThenPrintsOutputAndExitCode(t *testing.T) {
	cases := map[string]struct {
		stream     func(http.ResponseWriter)
		wantOut    string
		wantErr    string
		wantNotErr string
	}{
		"live output streams once": {
			stream: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, ":\n\n"+sseLog(1, "stdout", "hello live")+sseLog(2, "stderr", "warn live")+"event: end\ndata: {\"reason\":\"task_finished\"}\n\n")
			},
			wantOut: "hello live\n", wantErr: "warn live", wantNotErr: "incomplete",
		},
		"server without the stream prints stored output": {
			stream: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"not_found","title":"Not found","status":404}`))
			},
			wantOut: "hello from alpine\n", wantNotErr: "incomplete",
		},
		"interrupted stream warns instead of repeating output": {
			stream: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, sseLog(1, "stdout", "partial"))
			},
			wantOut: "partial\n", wantErr: "live output was incomplete",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var pulls []string
			srv := imageRunServer(t, &pulls, tc.stream)
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			out, readErr, restore := main.SwapIO(t)
			defer restore()
			if got := main.CmdRunImage([]string{"--image", "alpine", "--pull", "always", "sh", "-c", "exit 3"}); got != 3 {
				t.Fatalf("exit = %d, want the task's exit code 3", got)
			}
			stderr := readErr()
			if out.String() != tc.wantOut || !strings.Contains(stderr, tc.wantErr) ||
				(tc.wantNotErr != "" && strings.Contains(stderr, tc.wantNotErr)) {
				t.Fatalf("stdout = %q stderr = %q", out.String(), stderr)
			}
			if !reflect.DeepEqual(pulls, []string{"always", "missing"}) {
				t.Fatalf("pull policies = %q; only the first request may re-queue the image", pulls)
			}
		})
	}
}

// adr: 1330
func TestParseImageRunArgsInteractiveFlags(t *testing.T) {
	cases := map[string]struct {
		args             []string
		interactive, tty bool
		cmd              []string
	}{
		"-it":                    {args: []string{"-it", "--image", "alpine", "sh"}, interactive: true, tty: true, cmd: []string{"sh"}},
		"-ti":                    {args: []string{"--image", "alpine", "-ti", "sh"}, interactive: true, tty: true, cmd: []string{"sh"}},
		"-i -t":                  {args: []string{"-i", "-t", "--image", "alpine"}, interactive: true, tty: true},
		"long flags":             {args: []string{"--interactive", "--tty", "--image", "alpine"}, interactive: true, tty: true},
		"-i alone":               {args: []string{"-i", "--image", "alpine", "cat"}, interactive: true, cmd: []string{"cat"}},
		"-it after the command":  {args: []string{"--image", "alpine", "ls", "-it"}, cmd: []string{"ls", "-it"}},
		"-it after a terminator": {args: []string{"--image", "alpine", "--", "-it"}, cmd: []string{"-it"}},
		"no interactive flags":   {args: []string{"--image", "alpine", "true"}, cmd: []string{"true"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			opts, ok := main.ParseImageRunArgs(tc.args)
			req := (*opts.Request())
			var cmd []string
			if req.Args != nil {
				cmd = *req.Args
			}
			if !ok || req.Interactive != tc.interactive || req.TTY != tc.tty || !reflect.DeepEqual(cmd, tc.cmd) {
				t.Fatalf("parse(%q) = interactive=%v tty=%v args=%q ok=%v", tc.args, req.Interactive, req.TTY, cmd, ok)
			}
		})
	}

	_, _, restore := main.SwapIO(t)
	defer restore()
	for name, bad := range map[string][]string{
		"tty without interactive": {"-t", "--image", "alpine"},
		"interactive detached":    {"-i", "--detach", "--image", "alpine"},
		"interactive -d":          {"-it", "-d", "--image", "alpine"},
	} {
		if _, ok := main.ParseImageRunArgs(bad); ok {
			t.Errorf("%s: parse(%q) accepted", name, bad)
		}
	}
	previous := (*main.JsonOutput)
	(*main.JsonOutput) = true
	defer func() { (*main.JsonOutput) = previous }()
	if _, ok := main.ParseImageRunArgs([]string{"-i", "--image", "alpine"}); ok {
		t.Error("interactive with --json accepted")
	}
	if _, ok := main.ParseImageRunArgs([]string{"--image", "alpine"}); !ok {
		t.Error("--json without interactive rejected")
	}
}

// interactiveImageRunServer fakes POST /v1/image-runs, the attach gateway
// and cancellation for `gregale run -it --image`.
type interactiveImageRunServer struct {
	t         *testing.T
	mu        sync.Mutex
	request   api.CreateImageRunRequest
	attempts  int
	cancelled int
	stdin     []string
	attach    func(w http.ResponseWriter, r *http.Request, attempt int)
}

func (s *interactiveImageRunServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method + " " + r.URL.Path {
	case "POST /v1/image-runs":
		s.mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&s.request)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"queued","job":"ir-0123","image":"docker.io/library/alpine:latest","image_status":"ready","run_id":"run-1",` +
			`"command":["sh"],"attach":{"token":"tok","path":"/v1/jobs/ir-0123/runs/run-1/tasks/0/attach","subprotocol":"` + api.AppTaskAttachSubprotocol + `"}}`))
	case "GET /v1/jobs/ir-0123/runs/run-1/tasks/0/attach":
		if r.Header.Get(api.AppTaskAttachTokenHeader) != "tok" || r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "missing credentials", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.attempts++
		attempt := s.attempts
		s.mu.Unlock()
		s.attach(w, r, attempt)
	case "POST /v1/jobs/ir-0123/runs/run-1/cancel":
		s.mu.Lock()
		s.cancelled++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run_id":"run-1","cancelled":1}`))
	default:
		s.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func writeAttachProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	if status == http.StatusConflict {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"detail":%q,"status":%d}`, detail, status)
}

// adr: 1330
func TestRunInteractiveImageRelaysSessionAndExitCode(t *testing.T) {
	fake := &interactiveImageRunServer{t: t}
	fake.attach = func(w http.ResponseWriter, r *http.Request, attempt int) {
		if attempt == 1 {
			writeAttachProblem(w, http.StatusConflict, "the task VM is still starting")
			return
		}
		upgrader := websocket.Upgrader{Subprotocols: []string{api.AppTaskAttachSubprotocol}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{api.AppTaskAttachAttached})
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			fake.mu.Lock()
			fake.stdin = append(fake.stdin, string(msg))
			fake.mu.Unlock()
			if msg[0] == api.AppTaskAttachStdinClose {
				_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{api.AppTaskAttachStdout}, []byte("hello from sh\n")...))
				exit := 7
				terminal, _ := api.EncodeAppTaskAttachTerminal(api.AppTaskAttachTerminalMessage{Status: api.AppTaskStatusFailed, ExitCode: &exit})
				_ = conn.WriteMessage(websocket.BinaryMessage, terminal)
				return
			}
		}
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, readErr, restore := main.SwapIO(t)
	defer restore()
	oldIn := (*main.OsStdin)
	(*main.OsStdin) = strings.NewReader("echo hi\n")
	defer func() { (*main.OsStdin) = oldIn }()

	if got := main.CmdRunImage([]string{"-it", "--image", "alpine", "sh"}); got != 7 {
		t.Fatalf("exit = %d, want the command's exit code 7 (stderr %q)", got, readErr())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !fake.request.Interactive || fake.request.TTY {
		t.Fatalf("request = %+v; want interactive without a tty when stdin is not a terminal", fake.request)
	}
	if !strings.Contains(readErr(), "stdin is not a terminal") {
		t.Fatalf("stderr = %q; want the tty warning", readErr())
	}
	if fake.attempts != 2 || fake.cancelled != 0 {
		t.Fatalf("attach attempts = %d cancels = %d; want a retry after 409 and no cancel", fake.attempts, fake.cancelled)
	}
	if len(fake.stdin) != 2 || fake.stdin[0] != string(append([]byte{api.AppTaskAttachStdin}, "echo hi\n"...)) ||
		fake.stdin[1] != string([]byte{api.AppTaskAttachStdinClose}) {
		t.Fatalf("client messages = %q", fake.stdin)
	}
	if out.String() != "hello from sh\n" {
		t.Fatalf("stdout = %q", out.String())
	}
}

// adr: 1330
func TestRunInteractiveImageCancelsWhenItCannotAttach(t *testing.T) {
	cases := map[string]int{
		"old gateway":     http.StatusNotFound,
		"session refused": http.StatusGone,
	}
	for name, status := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &interactiveImageRunServer{t: t}
			fake.attach = func(w http.ResponseWriter, _ *http.Request, _ int) {
				writeAttachProblem(w, status, "the session cannot be attached")
			}
			srv := httptest.NewServer(fake)
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			_, readErr, restore := main.SwapIO(t)
			defer restore()
			oldIn := (*main.OsStdin)
			(*main.OsStdin) = strings.NewReader("")
			defer func() { (*main.OsStdin) = oldIn }()

			if got := main.CmdRunImage([]string{"-i", "--image", "alpine", "sh"}); got == 0 {
				t.Fatal("exit = 0 for a run that never attached")
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.attempts != 1 || fake.cancelled != 1 {
				t.Fatalf("attach attempts = %d cancels = %d; want one attempt and the run cancelled", fake.attempts, fake.cancelled)
			}
			if stderr := readErr(); !strings.Contains(stderr, "cannot be attached") {
				t.Fatalf("stderr = %q; want the gateway's detail", stderr)
			}
		})
	}
}

// adr: 1163
func TestSplitJobLogFollow(t *testing.T) {
	rest, follow := main.SplitJobLogFollow([]string{"nightly", "latest", "0", "-f"})
	if !follow || !reflect.DeepEqual(rest, []string{"nightly", "latest", "0"}) {
		t.Fatalf("split = %q, %v", rest, follow)
	}
	if _, follow := main.SplitJobLogFollow([]string{"nightly", "--max-bytes", "10"}); follow {
		t.Fatal("follow set without the flag")
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
		if got := main.ImageRunExitCode(tc.task); got != tc.want {
			t.Errorf("%s: exit = %d, want %d", name, got, tc.want)
		}
	}
}
