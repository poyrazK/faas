package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
)

func agentRequest() agentrunproto.Request {
	return agentrunproto.Request{Version: agentrunproto.Version, RunID: "r", Command: []string{"true"}, TimeoutSec: 5,
		MaxLogBytes: 1024, MaxResultBytes: 64, MaxOutputFiles: 2, MaxOutputBytes: 16}
}

func TestValidateAgentManifest(t *testing.T) {
	if err := validateAgentManifest([]byte(`{"kind":"agent","version":1}`)); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{"kind":"agent","version":2}`, `{"kind":"job","version":1}`, `{"kind":"agent","version":1} {}`,
		`{"kind":"agent","version":1,"command":["sh"]}`, `nope`} {
		if validateAgentManifest([]byte(data)) == nil {
			t.Errorf("accepted %s", data)
		}
	}
}

func TestStageAgentInputs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "input")
	files := []agentrunproto.File{{Path: "prompt.txt", Content: []byte("p")}, {Path: "steps/plan/out.json", Content: []byte("{}")}}
	if err := stageAgentInputs(dir, files); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f.Path))
		if err != nil || string(data) != string(f.Content) {
			t.Fatalf("%s: %q %v", f.Path, data, err)
		}
	}
	info, _ := os.Stat(filepath.Join(dir, "prompt.txt"))
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("input mode %v", info.Mode())
	}
	if err := stageAgentInputs(dir, []agentrunproto.File{{Path: "../escape"}}); err == nil {
		t.Fatal("escaping path staged")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); err == nil {
		t.Fatal("file written outside the input directory")
	}
}

func TestCollectAgentOutputs(t *testing.T) {
	write := func(t *testing.T, dir, name, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("result and files", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "result.json", `{"verdict":"ok"}`)
		write(t, dir, "reports/a.md", "aaaa")
		secret := filepath.Join(t.TempDir(), "secret")
		write(t, filepath.Dir(secret), "secret", "do not read")
		if err := os.Symlink(secret, filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		out, err := collectAgentOutputs(dir, agentRequest())
		if err != nil || out.failureCode != "" {
			t.Fatalf("%+v %v", out, err)
		}
		if string(out.result) != `{"verdict":"ok"}` || len(out.files) != 1 || out.files[0].Name != "reports/a.md" {
			t.Fatalf("outputs %+v (symlinks are never followed)", out)
		}
	})
	for name, files := range map[string]map[string]string{
		"result not an object": {"result.json": `[1]`},
		"result too big":       {"result.json": `{"x":"` + strings.Repeat("y", 64) + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for n, d := range files {
				write(t, dir, n, d)
			}
			out, err := collectAgentOutputs(dir, agentRequest())
			if err != nil || out.failureCode != agentrunproto.FailureInvalidResult || out.result != nil {
				t.Fatalf("%+v %v", out, err)
			}
		})
	}
	for name, files := range map[string]map[string]string{
		"too many files": {"a": "1", "b": "2", "c": "3"},
		"too many bytes": {"a": strings.Repeat("x", 17)},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for n, d := range files {
				write(t, dir, n, d)
			}
			out, err := collectAgentOutputs(dir, agentRequest())
			if err != nil || out.failureCode != agentrunproto.FailureOutputLimit || len(out.files) != 0 {
				t.Fatalf("%+v %v", out, err)
			}
		})
	}
	if out, err := collectAgentOutputs(filepath.Join(t.TempDir(), "missing"), agentRequest()); err != nil || out.result != nil {
		t.Fatalf("missing output dir: %+v %v", out, err)
	}
}

func TestAgentRunStatus(t *testing.T) {
	for _, tc := range []struct {
		payload JobExitPayload
		oom     bool
		status  agentrunproto.Status
		failure string
	}{
		{JobExitPayload{ErrorClass: "succeeded"}, false, agentrunproto.StatusSucceeded, ""},
		{JobExitPayload{ErrorClass: "timeout", ExitCode: 124}, true, agentrunproto.StatusTimedOut, ""},
		{JobExitPayload{ErrorClass: "cancelled", ExitCode: 143}, false, agentrunproto.StatusCancelled, ""},
		{JobExitPayload{ErrorClass: "infra", ExitCode: 137, Signal: 9}, true, agentrunproto.StatusOutOfMemory, ""},
		{JobExitPayload{ErrorClass: "oom", ExitCode: 137}, true, agentrunproto.StatusOutOfMemory, ""},
		{JobExitPayload{ErrorClass: "infra", ExitCode: 137, Signal: 9}, false, agentrunproto.StatusFailed, ""},
		{JobExitPayload{ErrorClass: "infra", ExitCode: 127}, false, agentrunproto.StatusFailed, agentrunproto.FailureInfra},
		{JobExitPayload{ErrorClass: "failed", ExitCode: 2}, true, agentrunproto.StatusFailed, ""},
	} {
		status, failure := agentRunStatus(tc.payload, tc.oom)
		if status != tc.status || failure != tc.failure {
			t.Errorf("%+v oom=%v: %s %q, want %s %q", tc.payload, tc.oom, status, failure, tc.status, tc.failure)
		}
	}
}

func TestOOMKillCount(t *testing.T) {
	vmstat := "nr_free_pages 100\noom_kill 3\npgfault 9\n"
	if n := oomKillCount(strings.NewReader(vmstat)); n != 3 {
		t.Fatalf("oom_kill = %d", n)
	}
	if n := oomKillCount(strings.NewReader("nr_free_pages 1\n")); n != 0 {
		t.Fatalf("missing counter = %d", n)
	}
	if runtime.GOOS == "linux" {
		f, err := os.Open("/proc/vmstat") //nolint:forbidigo // the kernel's own counters, not a customer path
		if err == nil {
			defer f.Close()
			_ = oomKillCount(f) // a real kernel's file parses
		}
	}
}

func TestAgentRunEnv(t *testing.T) {
	env := agentRunEnv("/home/node", map[string]string{"OPENAI_BASE_URL": "http://relay", "HOME": "/override"})
	slices.Sort(env)
	joined := strings.Join(env, "\n")
	for _, want := range []string{"HOME=/override", "OPENAI_BASE_URL=http://relay", "USER=app", "TMPDIR=/tmp"} {
		if !strings.Contains(joined, want) {
			t.Errorf("env lacks %s:\n%s", want, joined)
		}
	}
	if _, err := json.Marshal(env); err != nil {
		t.Fatal(err)
	}
}

// agentd builds model base URLs from agentrunproto's copy of the endpoint.
func TestAgentRunRelayEndpointMatchesGuestInit(t *testing.T) {
	if OutboundRelayEndpoint != agentrunproto.OutboundRelayEndpoint {
		t.Fatalf("guest-init serves %s but agentd points harnesses at %s", OutboundRelayEndpoint, agentrunproto.OutboundRelayEndpoint)
	}
	env := strings.Join(agentRunEnv("/home/app", map[string]string{"GREGALE_OUTBOUND_URL": "http://elsewhere"}), "\n")
	if !strings.Contains(env, "GREGALE_OUTBOUND_URL="+OutboundRelayEndpoint) || strings.Contains(env, "elsewhere") {
		t.Fatalf("relay URL not platform-owned: %s", env)
	}
}
