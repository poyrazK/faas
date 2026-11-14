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

// GA row 156: use the emitted commands with a real CLI. Recovery from each
// workflow mutation only reads the accepted run; latest stays resolved once.
func TestWorkflowWaitRecoveryResumesSameRun(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, action := range []string{"latest", "run", "resume", "retry"} {
		t.Run(action, func(t *testing.T) {
			const id = "11111111-1111-4111-8111-111111111111"
			var mutations, gets, lists, other atomic.Int32
			var finished atomic.Bool
			run := func(status string) map[string]string {
				return map[string]string{"id": id, "app_id": "22222222-2222-4222-8222-222222222222", "workflow_name": "orders", "status": status}
			}
			paths := map[string]string{"/v1/apps/api/workflows/orders/runs": "run", "/v1/workflows/runs/" + id + "/resume": "resume", "/v1/workflows/runs/" + id + "/steps/charge/retry": "retry"}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost && paths[r.URL.Path] == action {
					mutations.Add(1)
					_ = json.NewEncoder(w).Encode(run("running"))
					return
				}
				if r.Method != http.MethodGet || r.Header.Get("Idempotency-Key") != "" {
					other.Add(1)
					t.Errorf("recovery repeated a mutation or submission key: %s %s", r.Method, r.URL.Path)
					http.Error(w, "read-only recovery required", http.StatusMethodNotAllowed)
					return
				}
				switch r.URL.Path {
				case "/v1/capabilities":
					_, _ = w.Write([]byte(`{"hidden_capabilities":[]}`))
				case "/v1/apps/api/workflows/runs":
					lists.Add(1)
					if r.URL.Query().Get("workflow_name") != "orders" || r.URL.Query().Get("limit") != "1" {
						t.Error("latest lookup changed scope")
					}
					selected := run("running")
					if finished.Load() {
						selected["id"] = "33333333-3333-4333-8333-333333333333"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"runs": []any{selected}, "total": 1})
				case "/v1/workflows/runs/" + id:
					gets.Add(1)
					status := "running"
					if finished.Load() {
						status = "succeeded"
					}
					_ = json.NewEncoder(w).Encode(run(status))
				default:
					other.Add(1)
					t.Errorf("recovery changed its run: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
			profile := tg.exec(t, "fp_test_workflow-journey", tg.home, time.Minute, "config", "profile", "add", "staging", server.URL)
			if profile.exit != 0 {
				t.Fatalf("profile setup: %s", profile.stderr)
			}
			args := []string{"--profile", "staging", "--json", "workflow"}
			switch action {
			case "latest":
				args = append(args, "wait", "latest", "--app", "api", "--workflow-name", "orders")
			case "run":
				args = append(args, "run", "orders", "--app", "api", "--wait", "--idempotency-key", "journey-start-key")
			case "resume":
				args = append(args, "resume", id, "--expected-resume-count", "0", "--wait", "--idempotency-key", "journey-resume-key")
			case "retry":
				args = append(args, "retry", id, "charge", "--wait")
			}
			args = append(args, "--timeout", "250ms", "--poll-interval", "30s")
			result := tg.exec(t, "fp_test_workflow-journey", tg.home, time.Minute, args...)
			var problem struct {
				Code     string
				ExitCode int `json:"exit_code"`
				Recovery struct {
					RunID          string `json:"run_id"`
					InspectCommand string `json:"inspect_command"`
					ResumeCommand  string `json:"resume_command"`
				} `json:"recovery"`
			}
			var snapshot struct{ ID, Status string }
			if result.exit != 124 || json.Unmarshal(result.stderr, &problem) != nil || problem.Code != "workflow_wait_timeout" || problem.ExitCode != 124 ||
				problem.Recovery.RunID != id || problem.Recovery.InspectCommand != "gregale --profile staging workflow status "+id ||
				problem.Recovery.ResumeCommand != "gregale --profile staging workflow wait "+id+" --timeout 250ms --poll-interval 30s" ||
				json.Unmarshal(result.stdout, &snapshot) != nil || snapshot.ID != id || snapshot.Status != "running" {
				t.Fatalf("timeout lost usable recovery: exit=%d stdout=%s stderr=%s", result.exit, result.stdout, result.stderr)
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
				cmd.Env, cmd.Dir = tg.cliEnv("fp_test_workflow-journey"), tg.home
				for i, entry := range cmd.Env {
					if strings.HasPrefix(entry, "PATH=") {
						cmd.Env[i] = "PATH=" + bin + string(os.PathListSeparator) + strings.TrimPrefix(entry, "PATH=")
					}
				}
				var out, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &stderr
				err := cmd.Run()
				cancel()
				if err != nil || stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &snapshot) != nil || snapshot.ID != id || snapshot.Status != "succeeded" {
					t.Fatalf("recovery failed: %v stdout=%s stderr=%s", err, &out, &stderr)
				}
			}
			wantMutations, wantGets, wantLists := int32(1), int32(2), int32(0)
			if action == "latest" {
				wantMutations, wantGets, wantLists = 0, 3, 1
			}
			if mutations.Load() != wantMutations || gets.Load() != wantGets || lists.Load() != wantLists || other.Load() != 0 {
				t.Fatalf("recovery repeated work or reselected latest: mutations=%d gets=%d lists=%d other=%d", mutations.Load(), gets.Load(), lists.Load(), other.Load())
			}
		})
	}
}
