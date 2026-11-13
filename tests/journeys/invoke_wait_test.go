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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvokeAsyncWaitIsolatedAPI(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	for _, state := range []string{"completed", "dead_letter", "dispatching"} {
		t.Run(state, func(t *testing.T) {
			var posts, gets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "POST /v1/apps/api/invoke/async":
					posts.Add(1)
					_, _ = w.Write([]byte(`{"id":"inv-journey","status_url":"https://untrusted.invalid/never-follow"}`))
				case "GET /v1/invocations/inv-journey":
					gets.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "inv-journey", "state": state, "result": map[string]any{"ok": true}})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
			res := tg.exec(t, "fp_invoke-wait-private", tg.home, 10*time.Second, "--json", "invoke", "api", "--async", "--wait", "--interval", "1h", "--timeout", "250ms")
			want := 0
			if state == "dead_letter" {
				want = 1
			} else if state == "dispatching" {
				want = 124
			}
			var inv struct{ ID, State string }
			if res.exit != want || posts.Load() != 1 || gets.Load() != 1 || json.Unmarshal(res.stdout, &inv) != nil || inv.ID != "inv-journey" || inv.State != state {
				t.Fatalf("exit=%d posts=%d gets=%d stdout=%s stderr=%s", res.exit, posts.Load(), gets.Load(), res.stdout, res.stderr)
			}
			if want == 124 && !bytes.Contains(res.stderr, []byte("invocation wait inv-journey")) {
				t.Fatalf("missing accepted ID and resume command: %s", res.stderr)
			}
		})
	}
}

func TestInvokeAsyncWaitInterrupt(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	opened, closed := make(chan struct{}, 1), make(chan struct{}, 1)
	var posts, gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/apps/api/invoke/async":
			posts.Add(1)
			_, _ = w.Write([]byte(`{"id":"inv-interrupt"}`))
		case "GET /v1/invocations/inv-interrupt":
			gets.Add(1)
			opened <- struct{}{}
			<-r.Context().Done()
			closed <- struct{}{}
		default:
			t.Errorf("interrupt must not cancel remote work: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tg.cli, "--non-interactive", "--json", "invoke", "api", "--async", "--wait")
	cmd.Env, cmd.Dir = tg.cliEnv("fp_invoke-wait-private"), tg.home
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Stop and reap the child before closing the fake API on every exit path.
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("CLI did not poll the accepted invocation")
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) || exit.ExitCode() != 130 {
			t.Fatalf("interrupt returned %v, want exit 130: %s", waitErr, stderr.String())
		}
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("interrupt did not stop waiting")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("interrupt left the status request open")
	}
	var problem struct {
		Code, Detail, Hint string
		ExitCode           int `json:"exit_code"`
	}
	if json.Unmarshal(stderr.Bytes(), &problem) != nil || problem.Code != "invocation_wait_interrupted" || problem.ExitCode != 130 || !strings.Contains(problem.Detail, "inv-interrupt") || !strings.Contains(problem.Hint, "invocation wait inv-interrupt") || out.Len() != 0 || posts.Load() != 1 || gets.Load() != 1 {
		t.Fatalf("interrupted wait lost acceptance: posts=%d gets=%d stdout=%s stderr=%s", posts.Load(), gets.Load(), out.String(), stderr.String())
	}
}

// The live journey exercises one harmless GET through the durable queue and
// compares the CLI's final result with the stored invocation ledger record.
func TestInvokeAsyncWaitLiveAPI(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	slug, _ := tg.sharedProbe(t)
	res := tg.run(t, 3*time.Minute, "--json", "invoke", slug, "--async", "--wait", "--timeout", "2m", "--method", "GET", "--path", "/healthz")
	if res.exit != 0 {
		var problem struct{ Status int }
		if json.Unmarshal(res.stderr, &problem) == nil && problem.Status == http.StatusPaymentRequired {
			t.Skip("async invocation is unavailable for the journey account's plan")
		}
		t.Fatalf("async wait failed: exit=%d %s", res.exit, tg.redact(res.problem()))
	}
	var inv struct{ ID, State string }
	if json.Unmarshal(res.stdout, &inv) != nil || inv.ID == "" || inv.State != "completed" {
		t.Fatalf("missing final invocation: %s", res.stdout)
	}
	read := tg.ok(t, time.Minute, "--json", "invocation", "get", inv.ID)
	var stored struct{ ID, State string }
	if json.Unmarshal(read.stdout, &stored) != nil || stored != inv {
		t.Fatalf("wait result differs from the durable record: %s", read.stdout)
	}
}
