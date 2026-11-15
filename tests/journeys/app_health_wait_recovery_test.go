package journeys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// GA row 156: the built CLI's emitted commands keep the selected health scope
// and work from another directory, using only read-only API requests.
func TestAppHealthWaitRecoveryReadOnly(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, scenario := range []struct{ mode, environment string }{{"timeout", ""}, {"timeout", "staging"}, {"refusal", "staging"}, {"interrupt", "staging"}} {
		t.Run(scenario.mode+"/"+scenario.environment, func(t *testing.T) {
			if scenario.mode == "interrupt" && runtime.GOOS == "windows" {
				t.Skip("real SIGINT delivery requires POSIX signals")
			}
			const token = "fp_test_health-journey-private"
			var healthy atomic.Bool
			var reads, other atomic.Int32
			polled, canceled := make(chan struct{}), make(chan struct{})
			var polledOnce, canceledOnce sync.Once
			scope := scenario.environment
			if scope == "" {
				scope = "default"
			}
			first := api.AppHealthResponse{AppID: "11111111-1111-4111-8111-111111111111", Scope: scope, Status: "degraded", Phase: "serving",
				Summary: "Recorded health evidence", EvaluatedAt: time.Now().UTC().Format(time.RFC3339Nano), ValidForSeconds: 120}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("Authorization") != "Bearer "+token {
					other.Add(1)
					t.Errorf("health recovery changed credentials or mutated state: %s %s", r.Method, r.URL.Path)
					http.Error(w, "read-only health required", http.StatusMethodNotAllowed)
					return
				}
				if r.URL.Path == "/v1/capabilities" {
					_, _ = w.Write([]byte(`{"hidden_capabilities":[]}`))
					return
				}
				if r.URL.Path != "/v1/apps/demo/health" || r.URL.Query().Get("environment") != scenario.environment || scenario.environment == "" && r.URL.RawQuery != "" {
					other.Add(1)
					t.Errorf("health recovery changed scope: %s", r.URL)
					http.NotFound(w, r)
					return
				}
				n := reads.Add(1)
				if !healthy.Load() && n > 1 {
					switch scenario.mode {
					case "interrupt":
						polledOnce.Do(func() { close(polled) })
						<-r.Context().Done()
						canceledOnce.Do(func() { close(canceled) })
						return
					case "refusal":
						w.Header().Set("Content-Type", "application/problem+json")
						w.Header().Set(api.RequestIDHeader, "0123456789abcdef0123456789abcdef")
						w.WriteHeader(http.StatusForbidden)
						_ = json.NewEncoder(w).Encode(api.Problem{Status: 403, Code: "health_read_refused", Title: "Read refused", Hint: "Use an authorized profile"})
						return
					}
				}
				out := first
				if healthy.Load() {
					out.Status = "healthy"
					out.EvaluatedAt = time.Now().UTC().Format(time.RFC3339Nano)
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			t.Cleanup(server.Close)
			tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
			if setup := tg.exec(t, token, tg.home, time.Minute, "config", "profile", "add", "staging", server.URL); setup.exit != 0 {
				t.Fatalf("profile setup failed: %s", setup.stderr)
			}
			args := []string{"--profile", "staging", "--json", "app", "health", "demo", "--wait"}
			if scenario.environment != "" {
				args = append(args, "--env", scenario.environment)
			}
			timeout, interval, wantExit, wantReads := "200ms", "30s", 124, int32(1)
			if scenario.mode != "timeout" {
				timeout, interval, wantReads = "1m0s", "1s", 2
				wantExit = 6
				if scenario.mode == "interrupt" {
					wantExit = 130
				}
			}
			args = append(args, "--timeout", timeout, "--poll-interval", interval)
			var stdout, stderr []byte
			if scenario.mode == "interrupt" {
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, tg.cli, args...)
				cmd.Env, cmd.Dir = tg.cliEnv(token), tg.home
				var out, errOut bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &errOut
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				select {
				case <-polled:
				case err := <-done:
					t.Fatalf("CLI stopped before the interrupt: %v stderr=%s", err, &errOut)
				case <-ctx.Done():
					t.Fatal("CLI never polled health")
				}
				if err := cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
				var exited *exec.ExitError
				if err := <-done; !errors.As(err, &exited) || exited.ExitCode() != wantExit {
					t.Fatalf("interrupt exit changed: %v stdout=%s stderr=%s", err, &out, &errOut)
				}
				select {
				case <-canceled:
				case <-ctx.Done():
					t.Fatal("CLI left an active read after interruption")
				}
				stdout, stderr = out.Bytes(), errOut.Bytes()
			} else {
				result := tg.exec(t, token, tg.home, time.Minute, args...)
				if result.exit != wantExit {
					t.Fatalf("wait exit=%d stderr=%s", result.exit, result.stderr)
				}
				stdout, stderr = result.stdout, result.stderr
			}
			var problem struct {
				ExitCode int `json:"exit_code"`
				Recovery struct {
					App            string `json:"app"`
					Environment    string `json:"environment"`
					InspectCommand string `json:"inspect_command"`
					ResumeCommand  string `json:"resume_command"`
				} `json:"recovery"`
			}
			var snapshot api.AppHealthResponse
			inspect := "gregale --profile staging app health --app demo"
			if scenario.environment != "" {
				inspect += " --env " + scenario.environment
			}
			if json.Unmarshal(stderr, &problem) != nil || problem.ExitCode != wantExit || problem.Recovery.App != "demo" || problem.Recovery.Environment != scenario.environment ||
				problem.Recovery.InspectCommand != inspect || problem.Recovery.ResumeCommand != inspect+" --wait --timeout "+timeout+" --poll-interval "+interval ||
				json.Unmarshal(stdout, &snapshot) != nil || snapshot.EvaluatedAt != first.EvaluatedAt || snapshot.Status != "degraded" || strings.Contains(string(stderr), token) {
				t.Fatalf("wait lost usable recovery or last assessment: stdout=%s stderr=%s", stdout, stderr)
			}
			healthy.Store(true)
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
				cmd.Env, cmd.Dir = tg.cliEnv(token), t.TempDir()
				for i, entry := range cmd.Env {
					if strings.HasPrefix(entry, "PATH=") {
						cmd.Env[i] = "PATH=" + bin + string(os.PathListSeparator) + strings.TrimPrefix(entry, "PATH=")
					}
				}
				var out, errOut bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &errOut
				err := cmd.Run()
				cancel()
				if err != nil || errOut.Len() != 0 || json.Unmarshal(out.Bytes(), &snapshot) != nil || snapshot.Status != "healthy" || snapshot.Scope != scope {
					t.Fatalf("emitted command failed: %v stdout=%s stderr=%s", err, &out, &errOut)
				}
			}
			if reads.Load() != wantReads+2 || other.Load() != 0 {
				t.Fatalf("recovery changed target or repeated work: reads=%d other=%d", reads.Load(), other.Load())
			}
		})
	}
}
