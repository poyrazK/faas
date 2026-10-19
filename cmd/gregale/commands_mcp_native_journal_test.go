package main_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func journalFixture() main.McpNativeReleaseState {
	return main.McpNativeReleaseState{Version: 1, Fingerprint: "fixture", Stage: "prepared", PreviousDeployments: map[string]string{}, Parked: map[string]bool{}}
}

func TestMCPNativeJournalRevisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	s := journalFixture()
	staleInitial := s
	if err := main.SaveMCPNativeState(path, &s); err != nil {
		t.Fatal(err)
	}
	if s.Revision != 1 {
		t.Fatalf("revision=%d", s.Revision)
	}
	if err := main.SaveMCPNativeState(path, &staleInitial); err == nil {
		t.Fatal("stale creation overwrote journal")
	}
	stale, err := main.LoadMCPNativeState(path, s.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	s.Stage = "retirement_pending"
	if err := main.SaveMCPNativeState(path, &s); err != nil {
		t.Fatal(err)
	}
	stale.Stage = "quarantined"
	if err := main.SaveMCPNativeState(path, &stale); err == nil {
		t.Fatal("stale writer downgraded retirement")
	}
	current, err := main.LoadMCPNativeState(path, s.Fingerprint)
	if err != nil || current.Stage != "retirement_pending" || current.Revision != 2 {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := main.SaveMCPNativeState(path, &s); err == nil {
		t.Fatal("stale writer resurrected removed journal")
	}
}

func TestMCPNativeJournalLegacyUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	s := journalFixture()
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := main.LoadMCPNativeState(path, s.Fingerprint)
	if err != nil || loaded.Revision != 0 {
		t.Fatalf("legacy=%+v err=%v", loaded, err)
	}
	loaded.Stage = "web_restored"
	if err := main.SaveMCPNativeState(path, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 1 {
		t.Fatal("legacy journal did not advance")
	}
}

func TestMCPNativeJournalOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	owner, err := main.OwnMCPJournal(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	token := os.Getenv(main.McpJournalOwnerEnv)
	if _, err := main.OwnMCPJournal(path, false); !errors.Is(err, (*main.ErrMCPJournalBusy)) {
		t.Fatalf("second owner: %v", err)
	}
	t.Setenv(main.McpJournalOwnerEnv, strings.Repeat("0", 64))
	if _, err := main.OwnMCPJournal(path, true); err == nil {
		t.Fatal("foreign adapter accepted")
	}
	t.Setenv(main.McpJournalOwnerEnv, token)
	adapter, err := main.OwnMCPJournal(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter()
	// Simulate parent loss during a still-running adapter. Its handoff lock must
	// prevent takeover until the adapter has finished.
	owner()
	if _, err := main.OwnMCPJournal(path, false); !errors.Is(err, (*main.ErrMCPJournalBusy)) {
		t.Fatalf("takeover during adapter: %v", err)
	}
	adapter()
	replacement, err := main.OwnMCPJournal(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement()
	t.Setenv(main.McpJournalOwnerEnv, token)
	if _, err := main.OwnMCPJournal(path, true); err == nil {
		t.Fatal("stale adapter accepted by replacement owner")
	}
}

// Subprocesses exercise OS lock release on abrupt exit and token inheritance.
func TestMCPNativeJournalProcessHelper(t *testing.T) {
	mode := os.Getenv("MCP_JOURNAL_TEST_MODE")
	if mode == "" {
		return
	}
	path := os.Getenv("MCP_JOURNAL_TEST_PATH")
	release, err := main.OwnMCPJournal(path, mode == "adapter")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if mode == "adapter" {
		s, err := main.LoadMCPNativeState(path, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		s.Stage = "web_restored"
		if err := main.SaveMCPNativeState(path, &s); err != nil {
			t.Fatal(err)
		}
		return
	}
	if mode == "writer" {
		state, err := main.LoadMCPNativeState(path, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		state.WorkerIDs = []string{strings.Repeat("worker", 200000)}
		fmt.Println("ready")
		for i := 0; ; i++ {
			state.Stage = fmt.Sprintf("write-%d", i)
			if err := main.SaveMCPNativeState(path, &state); err != nil {
				t.Fatal(err)
			}
		}
	}
	fmt.Println("ready")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	// Leave the lock file behind and skip deferred unlock, as in a process crash.
	os.Exit(0)
}

func journalProcess(t *testing.T, mode, path string) *exec.Cmd {
	t.Helper()
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMCPNativeJournalProcessHelper$")
	command.Env = append(os.Environ(), "MCP_JOURNAL_TEST_MODE="+mode, "MCP_JOURNAL_TEST_PATH="+path)
	return command
}

func TestMCPNativeJournalAdapterSubprocess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	owner, err := main.OwnMCPJournal(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	s := journalFixture()
	if err := main.SaveMCPNativeState(path, &s); err != nil {
		t.Fatal(err)
	}
	output, err := journalProcess(t, "adapter", path).CombinedOutput()
	if err != nil {
		t.Fatalf("adapter: %s %v", output, err)
	}
	if err := main.SaveMCPNativeState(path, &s); err == nil {
		t.Fatal("parent overwrote adapter's update")
	}
	current, err := main.LoadMCPNativeState(path, s.Fingerprint)
	if err != nil || current.Revision != 2 || current.Stage != "web_restored" {
		t.Fatalf("current=%+v err=%v", current, err)
	}
}

func TestMCPNativeJournalOwnerCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	command := journalProcess(t, "owner", path)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = command.Wait() }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("owner: %s %s %v", line, stderr.String(), err)
	}
	if _, err := main.OwnMCPJournal(path, false); !errors.Is(err, (*main.ErrMCPJournalBusy)) {
		t.Fatalf("concurrent process admitted: %v", err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	owner, err := main.OwnMCPJournal(path, false)
	if err != nil {
		t.Fatalf("crashed owner left a permanent lock: %v", err)
	}
	owner()
	if _, err := main.OwnMCPJournal(path, true); err == nil {
		t.Fatal("orphan adapter accepted")
	}
}

func TestMCPNativeJournalInterruptedWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	original := journalFixture()
	if err := main.SaveMCPNativeState(path, &original); err != nil {
		t.Fatal(err)
	}
	command := journalProcess(t, "writer", path)
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("writer: %s %s %v", line, stderr.String(), err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".mcp-release-*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writer did not begin a journal replacement")
		}
		time.Sleep(time.Millisecond)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	current, err := main.LoadMCPNativeState(path, "fixture")
	if err != nil || current.Revision < 1 {
		t.Fatalf("interrupted write corrupted journal: %+v %v", current, err)
	}
	if current.Stage != "prepared" && !strings.HasPrefix(current.Stage, "write-") {
		t.Fatalf("partial state: %+v", current)
	}
	owner, err := main.OwnMCPJournal(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	current.Stage = "recovered"
	if err := main.SaveMCPNativeState(path, &current); err != nil {
		t.Fatalf("could not resume after interrupted write: %v", err)
	}
}

func TestMCPNativeJournalCommandOwnership(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"web", "worker"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	plan := filepath.Join(root, "plan.json")
	if err := os.WriteFile(plan, []byte(`{"web_app":"web","worker_app":"worker","observer_app":"observer","observer_metric_app":"observer","web_path":"web","worker_path":"worker","timeout_seconds":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "journal.json")
	p, err := main.ReadMCPNativePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := main.McpNativeFingerprint(p)
	if err != nil {
		t.Fatal(err)
	}
	s := journalFixture()
	s.Fingerprint = fingerprint
	if err := main.SaveMCPNativeState(path, &s); err != nil {
		t.Fatal(err)
	}
	owner, err := main.OwnMCPJournal(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	var output bytes.Buffer
	(*main.OsStdout), (*main.JsonOutput) = &output, true
	defer func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON }()
	for _, action := range []string{"run", "restore", "quarantine", "retire", "recover"} {
		output.Reset()
		args := []string{action, "--plan", plan, "--state", path}
		if action == "recover" {
			args = append(args, "--resume")
		}
		stderr, restore := main.CaptureStderr(t)
		code := main.CmdMCP(append([]string{"tasks", "release"}, args...))
		restore()
		if code == 0 || !strings.Contains(stderr.String(), "owned by another command") {
			t.Fatalf("%s bypassed ownership: code=%d output=%s", action, code, stderr.String())
		}
	}
	output.Reset()
	if code := main.CmdMCP([]string{"tasks", "release", "status", "--plan", plan, "--state", path}); code != 0 {
		t.Fatalf("status blocked by ownership: %s", output.String())
	}
}
