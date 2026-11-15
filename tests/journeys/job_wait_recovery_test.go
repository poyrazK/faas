package journeys

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: execute the emitted read-only recovery commands with the built
// CLI, retaining the originally selected run even when latest has advanced.
func TestJobWaitRecoveryResumesSameRun(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, submitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone-latest", true: "submitted"}[submitted], func(t *testing.T) {
			testJobWaitRecoveryResumesSameRun(t, cli, submitted)
		})
	}
}

func testJobWaitRecoveryResumesSameRun(t *testing.T, cli string, submitted bool) {
	const runID = "11111111-1111-4111-8111-111111111111"
	const jobID = "22222222-2222-4222-8222-222222222222"
	var posts, polls, lists, unexpected atomic.Int32
	var finished atomic.Bool
	run := func() map[string]any {
		status := "queued"
		if finished.Load() {
			status = "succeeded"
		}
		return map[string]any{"id": runID, "job_id": jobID, "account_id": "journey-account", "aggregate_status": status, "tasks": 1}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.Header.Get("Idempotency-Key") != "" {
			t.Error("read-only recovery inherited a submission key")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/capabilities":
			_, _ = w.Write([]byte(`{"hidden_capabilities":[]}`))
		case "GET /v1/jobs/nightly":
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "nightly", "id": jobID, "account_id": "journey-account"})
		case "POST /v1/jobs/nightly/runs":
			posts.Add(1)
			_ = json.NewEncoder(w).Encode(run())
		case "GET /v1/jobs/nightly/runs":
			lists.Add(1)
			candidate := run()
			if finished.Load() {
				candidate["id"] = "33333333-3333-4333-8333-333333333333"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"runs": []any{candidate}})
		case "GET /v1/jobs/nightly/runs/" + runID:
			polls.Add(1)
			_ = json.NewEncoder(w).Encode(run())
		default:
			unexpected.Add(1)
			t.Errorf("recovery changed durable work or target: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
	profile := tg.exec(t, "fp_test_job-wait-journey", tg.home, time.Minute, "config", "profile", "add", "staging", server.URL)
	if profile.exit != 0 {
		t.Fatalf("profile setup: %s", profile.stderr)
	}
	args := []string{"--profile", "staging", "--json", "job", "wait", "nightly", "latest", "--timeout", "250ms", "--poll-interval", "1h"}
	if submitted {
		args = []string{"--profile", "staging", "--json", "job", "run", "nightly", "--tasks", "1", "--wait", "--wait-timeout", "250ms", "--poll-interval", "1h"}
	}
	result := tg.exec(t, "fp_test_job-wait-journey", tg.home, time.Minute, args...)
	var problem struct {
		Code     string
		ExitCode int `json:"exit_code"`
		Recovery struct {
			Job            string `json:"job"`
			RunID          string `json:"run_id"`
			InspectCommand string `json:"inspect_command"`
			ResumeCommand  string `json:"resume_command"`
		} `json:"recovery"`
	}
	var receipt struct {
		Run      struct{ ID string } `json:"run"`
		ExitCode int                 `json:"exit_code"`
	}
	if result.exit != 124 || json.Unmarshal(result.stderr, &problem) != nil || problem.Code != "job_wait_timeout" || problem.ExitCode != 124 || problem.Recovery.Job != "nightly" || problem.Recovery.RunID != runID ||
		json.Unmarshal(result.stdout, &receipt) != nil || receipt.Run.ID != runID || receipt.ExitCode != 124 || polls.Load() != 1 {
		t.Fatalf("wait lost the accepted run: exit=%d stdout=%s stderr=%s", result.exit, result.stdout, result.stderr)
	}
	finished.Store(true)
	bin := filepath.Join(tg.home, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := exec.LookPath(tg.cli)
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(bin, "gregale")); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{problem.Recovery.InspectCommand, problem.Recovery.ResumeCommand} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Env, cmd.Dir = tg.cliEnv("fp_test_job-wait-journey"), tg.home
		for i, entry := range cmd.Env {
			if strings.HasPrefix(entry, "PATH=") {
				cmd.Env[i] = "PATH=" + bin + string(os.PathListSeparator) + strings.TrimPrefix(entry, "PATH=")
			}
		}
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		cancel()
		var response struct {
			ID  string
			Run *struct{ ID string }
		}
		if err != nil || stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &response) != nil || response.ID != runID && (response.Run == nil || response.Run.ID != runID) {
			t.Fatalf("recovery command failed: %v stdout=%s stderr=%s", err, &out, &stderr)
		}
	}
	wantPosts, wantLists := int32(0), int32(1)
	if submitted {
		wantPosts, wantLists = 1, 0
	}
	if posts.Load() != wantPosts || lists.Load() != wantLists || polls.Load() != 3 || unexpected.Load() != 0 {
		t.Fatalf("recovery resubmitted, canceled or reselected latest: posts=%d lists=%d polls=%d unexpected=%d", posts.Load(), lists.Load(), polls.Load(), unexpected.Load())
	}
}
