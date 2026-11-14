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

// GA row 156: image-wait recovery only reads readiness, even after preparation
// succeeds. Continuing the pre-submission wait never submits the deferred run.
func TestJobImageWaitRecoveryIsReadOnly(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "get", true: "run"}[before], func(t *testing.T) {
			const jobID = "22222222-2222-4222-8222-222222222222"
			const imageRef = "registry.example/nightly:v1"
			var reads, mutations atomic.Int32
			var ready atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet || r.Header.Get("Idempotency-Key") != "" {
					mutations.Add(1)
					t.Errorf("readiness recovery attempted mutation: %s %s", r.Method, r.URL.Path)
					http.Error(w, "read-only fixture", http.StatusMethodNotAllowed)
					return
				}
				switch r.URL.Path {
				case "/v1/capabilities":
					_, _ = w.Write([]byte(`{"hidden_capabilities":[]}`))
				case "/v1/jobs/nightly":
					reads.Add(1)
					status := "pending"
					if ready.Load() {
						status = "ready"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": jobID, "account_id": "journey-account", "name": "nightly",
						"status": "active", "image_ref": imageRef, "image_materialization_status": status})
				default:
					t.Errorf("readiness recovery changed target: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
			profile := tg.exec(t, "fp_test_image-wait-journey", tg.home, time.Minute, "config", "profile", "add", "staging", server.URL)
			if profile.exit != 0 {
				t.Fatalf("profile setup: %s", profile.stderr)
			}
			args := []string{"--profile", "staging", "--json", "job", "get", "nightly", "--wait-ready", "--timeout", "250ms", "--poll-interval", "1h"}
			if before {
				args = []string{"--profile", "staging", "--json", "job", "run", "nightly", "--tasks", "1", "--wait-ready", "--image-wait-timeout", "250ms", "--image-poll-interval", "1h"}
			}
			result := tg.exec(t, "fp_test_image-wait-journey", tg.home, time.Minute, args...)
			var problem struct {
				Code     string
				ExitCode int `json:"exit_code"`
				Recovery struct {
					Job            string
					Phase          string
					Submitted      *bool  `json:"submitted"`
					JobID          string `json:"job_id"`
					ImageRef       string `json:"image_ref"`
					InspectCommand string `json:"inspect_command"`
					ResumeCommand  string `json:"resume_command"`
				} `json:"recovery"`
			}
			if result.exit != 124 || json.Unmarshal(result.stderr, &problem) != nil || problem.Code != "job_image_wait_timeout" || problem.ExitCode != 124 ||
				problem.Recovery.Job != "nightly" || problem.Recovery.JobID != jobID || problem.Recovery.ImageRef != imageRef || problem.Recovery.Phase != "image_wait" ||
				problem.Recovery.Submitted == nil || *problem.Recovery.Submitted || reads.Load() != 1 || mutations.Load() != 0 {
				t.Fatalf("image wait lost phase or submission state: exit=%d stdout=%s stderr=%s", result.exit, result.stdout, result.stderr)
			}
			ready.Store(true)
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
				if command == "" {
					t.Fatal("known pending image omitted recovery command")
				}
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				cmd := exec.CommandContext(ctx, "sh", "-c", command)
				cmd.Env, cmd.Dir = tg.cliEnv("fp_test_image-wait-journey"), tg.home
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
					Job *struct{ ID string }
				}
				if err != nil || stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &response) != nil || response.ID != jobID && (response.Job == nil || response.Job.ID != jobID) {
					t.Fatalf("readiness recovery failed: %v stdout=%s stderr=%s", err, &out, &stderr)
				}
			}
			if mutations.Load() != 0 || reads.Load() != 3 {
				t.Fatalf("readiness recovery submitted or repeated work: reads=%d mutations=%d", reads.Load(), mutations.Load())
			}
		})
	}
}
