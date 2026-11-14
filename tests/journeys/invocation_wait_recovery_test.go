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

func TestInvocationWaitRecoveryResumesSameCall(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	for _, tc := range []struct{ id, argument string }{
		{"inv~resume", "'inv~resume'"},
		{"--replay", "-- --replay"},
	} {
		t.Run(tc.id, func(t *testing.T) { testInvocationWaitRecoveryResumesSameCall(t, configured, tc.id, tc.argument) })
	}
}

func testInvocationWaitRecoveryResumesSameCall(t *testing.T, configured *target, id, argument string) {
	var posts, gets atomic.Int32
	var finished atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/apps/api/invoke/async":
			posts.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
		case "GET /v1/invocations/" + id:
			gets.Add(1)
			state := "pending"
			if finished.Load() {
				state = "completed"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "state": state})
		default:
			t.Errorf("recovery must only read the original invocation: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
	profile := tg.exec(t, "fp_recovery-private", tg.home, 5*time.Second, "config", "profile", "add", "staging", server.URL)
	if profile.exit != 0 {
		t.Fatalf("could not prepare selected profile: %s", profile.stderr)
	}
	res := tg.exec(t, "fp_recovery-private", tg.home, 10*time.Second,
		"--profile", "staging", "--json", "invocation", "run", "api", "--wait", "--timeout", "250ms", "--interval", "1h")
	var problem struct {
		Code     string
		ExitCode int `json:"exit_code"`
		Recovery struct {
			InvocationID   string `json:"invocation_id"`
			InspectCommand string `json:"inspect_command"`
			ResumeCommand  string `json:"resume_command"`
		} `json:"recovery"`
	}
	if res.exit != 124 || json.Unmarshal(res.stderr, &problem) != nil || problem.Code != "invocation_wait_timeout" || problem.ExitCode != 124 ||
		problem.Recovery.InvocationID != id ||
		problem.Recovery.InspectCommand != "gregale --profile staging invocation get "+argument ||
		problem.Recovery.ResumeCommand != "gregale --profile staging invocation wait "+argument || posts.Load() != 1 || gets.Load() != 1 {
		t.Fatalf("missing usable recovery: exit=%d posts=%d gets=%d stdout=%s stderr=%s", res.exit, posts.Load(), gets.Load(), res.stdout, res.stderr)
	}
	finished.Store(true)
	// Put the built CLI on PATH, then execute the actual POSIX-quoted commands.
	bin := filepath.Join(tg.home, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	cliPath, err := exec.LookPath(tg.cli)
	if err != nil {
		t.Fatal(err)
	}
	cliPath, err = filepath.Abs(cliPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cliPath, filepath.Join(bin, "gregale")); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{problem.Recovery.InspectCommand, problem.Recovery.ResumeCommand} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Env, cmd.Dir = tg.cliEnv("fp_recovery-private"), tg.home
		for i, entry := range cmd.Env {
			if strings.HasPrefix(entry, "PATH=") {
				cmd.Env[i] = "PATH=" + bin + string(os.PathListSeparator) + strings.TrimPrefix(entry, "PATH=")
			}
		}
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		cancel()
		var inv struct{ ID, State string }
		if err != nil || json.Unmarshal(out.Bytes(), &inv) != nil || inv.ID != id || inv.State != "completed" || stderr.Len() != 0 {
			t.Fatalf("recovery failed: %v stdout=%s stderr=%s", err, out.String(), stderr.String())
		}
	}
	if posts.Load() != 1 || gets.Load() != 3 {
		t.Fatalf("recovery resubmitted or lost the original call: posts=%d gets=%d", posts.Load(), gets.Load())
	}
}
