//go:build !no_pg && !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Separate test processes exercise the public command routing, process inventory,
// signal handling and ownership verifier. They never simulate an API response.
func TestScenarioCleanupWatchProcess(t *testing.T) {
	// adr: 984 — acceptance process helpers remain inert outside an explicit child invocation.
	switch os.Getenv("GREGALE_WATCH_ACCEPTANCE_ROLE") {
	case "watcher", "cleanup", "status":
		var args []string
		if err := json.NewDecoder(os.Stdin).Decode(&args); err != nil {
			t.Fatal(err)
		}
		os.Exit(cmdTestPolicy(args))
	case "worker":
		// The coordinator is deliberately killed first, leaving this tracked process alive.
		select {
		case <-time.After(2 * time.Minute):
			t.Fatal("worker was not terminated")
		}
	case "coordinator":
		directory := os.Getenv("GREGALE_WATCH_ACCEPTANCE_DIRECTORY")
		var plan testCampaignPolicyPlan
		if err := readTestCampaignJSON(os.Getenv("GREGALE_WATCH_ACCEPTANCE_PLAN"), &plan); err != nil {
			t.Fatal(err)
		}
		release, err := acquireTestCampaignRunnerLock(directory, testCampaignJSONHash(plan))
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		worker := exec.Command(binary, "-test.run=^TestScenarioCleanupWatchProcess$", "-test.timeout=2m")
		worker.Env = append(os.Environ(), "GREGALE_WATCH_ACCEPTANCE_ROLE=worker")
		if err := runTestCampaignTrackedWorker(worker, directory, "a"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScenarioConsumerPostgresCleanupWatch(t *testing.T) {
	// adr: 984 — real process deaths and HTTP/SQL outages cannot turn cleanup uncertainty into success.
	if runtime.GOOS != "linux" {
		t.Skip("process identity acceptance requires Linux")
	}
	for _, mode := range []string{"api-unavailable", "database-unavailable", "restart", "restart-in-flight", "restart-twice", "missing-receipt", "changed-progress", "changed-checkpoint", "changed-handoff", "concurrent"} {
		t.Run(mode, func(t *testing.T) { testConsumerPostgresPortableRecovery(t, false, false, "watch:"+mode) })
	}
}

type cleanupWatchAcceptanceProcess struct {
	cmd    *exec.Cmd
	events chan json.RawMessage
	done   chan error
	once   sync.Once
	err    error
	output bytes.Buffer
}

func startCleanupWatchAcceptanceProcess(t *testing.T, role string, args []string, env ...string) *cleanupWatchAcceptanceProcess {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	p := &cleanupWatchAcceptanceProcess{events: make(chan json.RawMessage, 512), done: make(chan error, 1)}
	p.cmd = exec.CommandContext(ctx, binary, "-test.run=^TestScenarioCleanupWatchProcess$", "-test.timeout=90s")
	p.cmd.Env = append(os.Environ(), "GREGALE_WATCH_ACCEPTANCE_ROLE="+role)
	p.cmd.Env = append(p.cmd.Env, env...)
	body, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	p.cmd.Stdin = bytes.NewReader(body)
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.cmd.Stderr = &p.output
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		decoder := json.NewDecoder(stdout)
		for {
			var event json.RawMessage
			err := decoder.Decode(&event)
			if err != nil {
				break
			}
			p.events <- event
		}
		close(p.events)
		p.done <- p.cmd.Wait()
	}()
	t.Cleanup(func() { _ = p.cmd.Process.Kill(); p.wait(t, -1) })
	return p
}

func (p *cleanupWatchAcceptanceProcess) wait(t *testing.T, code int) {
	t.Helper()
	p.once.Do(func() {
		select {
		case p.err = <-p.done:
		case <-time.After(15 * time.Second):
			t.Fatal("acceptance process did not exit")
		}
	})
	if code < 0 {
		return
	}
	got := 0
	if p.err != nil {
		var exited *exec.ExitError
		if !errors.As(p.err, &exited) {
			t.Fatal(p.err)
		}
		got = exited.ExitCode()
	}
	if got != code {
		t.Fatalf("acceptance process exit=%d want=%d; %s", got, code, p.output.String())
	}
}

func (p *cleanupWatchAcceptanceProcess) killed(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	p.verifyKilled(t)
}

func (p *cleanupWatchAcceptanceProcess) verifyKilled(t *testing.T) {
	t.Helper()
	p.wait(t, -1)
	var exited *exec.ExitError
	if !errors.As(p.err, &exited) {
		t.Fatal("process did not die from SIGKILL")
	}
	status, ok := exited.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("process death was not SIGKILL")
	}
}

func (p *cleanupWatchAcceptanceProcess) state(t *testing.T, want string) time.Time {
	t.Helper()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case raw, ok := <-p.events:
			if !ok {
				p.wait(t, -1)
				t.Fatalf("process exited before %s: %s", want, p.output.String())
			}
			var event struct {
				State string    `json:"watch_state"`
				At    time.Time `json:"observed_at"`
			}
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if event.State == want {
				return event.At
			}
		case <-timer.C:
			t.Fatalf("watcher did not report %s", want)
		}
	}
}

func testConsumerPostgresCleanupWatch(t *testing.T, mode string, b *consumerAcceptanceBackend, d *consumerAcceptanceDelivery, endpoint, root, manifest, work string, journals []testCampaignRunJournal, key api.ConsumerKeyResponse) {
	t.Helper()
	plan := filepath.Join(root, "artifacts", "plan.json")
	env := []string{"FAAS_API=" + endpoint, "FAAS_TOKEN=" + b.token}
	coordinator := startCleanupWatchAcceptanceProcess(t, "coordinator", nil, "GREGALE_WATCH_ACCEPTANCE_DIRECTORY="+work, "GREGALE_WATCH_ACCEPTANCE_PLAN="+plan)
	var lock testCampaignRunnerLock
	var record testCampaignWorkerProcess
	waitCleanupWatchCondition(t, func() bool {
		if readTestCampaignJSON(filepath.Join(work, "runner.lock"), &lock) != nil {
			return false
		}
		return readTestCampaignJSON(filepath.Join(testCampaignProcessDirectory(work, lock), "a.json"), &record) == nil && record.State == "started" && record.Process != nil
	})
	worker, err := os.FindProcess(record.Process.PID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if status, _ := probeTestCampaignProcess(*record.Process); status == "alive" || status == "stopped" {
			_ = worker.Kill()
		}
		_ = worker.Release()
	})
	args := []string{"cleanup-watch", "--plan", plan, "--policy", filepath.Join(root, "artifacts", "policy.yaml"), "--manifest", manifest, "--directory", work, "--handoff", filepath.Join(root, "bundle-index.json"), "--execute", "--interval", "1s", "--grace", "2s", "--attempts", "30"}
	watcher := startCleanupWatchAcceptanceProcess(t, "watcher", args, env...)
	watcher.state(t, "waiting_for_owner")
	coordinator.killed(t)
	// Two observations, separated by a complete polling interval, prove that an
	// orphan worker continues to block even after its coordinator has died.
	watcher.state(t, "waiting_for_owner")
	watcher.state(t, "waiting_for_owner")
	assertConsumerAcceptanceKey(t, b, journals[2].Faults.Consumers["customer"].AppID, key, true)
	before, err := os.ReadFile(filepath.Join(work, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cp testCampaignCheckpoint
	var p testCampaignPolicyPlan
	if err := readTestCampaignJSON(plan, &p); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(before, &cp); err != nil {
		t.Fatal(err)
	}
	assessment, err := inspectTestCampaignRunnerLock(work, p, cp)
	if err != nil || assessment.Recoverable || assessment.Coordinator.Status != "terminated" || len(assessment.Workers) != 1 || assessment.Workers[0].Status != "alive" {
		t.Fatal("orphan ownership protection failed", err)
	}
	d.mu.Lock()
	cancels := d.cancels
	d.mu.Unlock()
	if cancels != 0 {
		t.Fatal("watcher mutated resources owned by a live worker")
	}
	setOutage := func(active bool) {
		if b.databaseProxy != nil {
			b.databaseProxy.setOnline(!active)
		} else {
			d.mu.Lock()
			d.unavailable = active
			d.mu.Unlock()
		}
	}
	setOutage(true)
	if err := worker.Kill(); err != nil {
		t.Fatal(err)
	}
	waitCleanupWatchCondition(t, func() bool { status, _ := probeTestCampaignProcess(*record.Process); return status == "terminated" })
	graceAt := watcher.state(t, "owner_terminated_grace_period")
	during, err := os.ReadFile(filepath.Join(work, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, during) {
		t.Fatal("grace period changed checkpoint")
	}
	unresolvedAt := watcher.state(t, "cleanup_unresolved")
	if unresolvedAt.Sub(graceAt) < 2*time.Second {
		t.Fatal("cleanup skipped the ownership grace period")
	}
	assertConsumerAcceptanceKey(t, b, journals[2].Faults.Consumers["customer"].AppID, key, true)
	assertCleanupWatchOutage(t, work)
	// Freeze the live watcher between attempts so status must remain purely
	// observational while the kernel guard and stopped owner remain protected.
	if err := watcher.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	var watchReceipt testCampaignWatchReceipt
	if err := readTestCampaignJSON(filepath.Join(work, testCampaignWatchReceiptName), &watchReceipt); err != nil {
		t.Fatal(err)
	}
	waitCleanupWatchCondition(t, func() bool { state, _ := probeTestCampaignProcess(watchReceipt.Owner); return state == "stopped" })
	testCleanupWatchPublicStatus(t, d, env, root, manifest, work, "active")
	if err := watcher.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	if mode == "concurrent" {
		other := startCleanupWatchAcceptanceProcess(t, "watcher", args, env...)
		other.wait(t, 1)
		var receipt testCampaignWatchReceipt
		if err := readTestCampaignJSON(filepath.Join(work, testCampaignWatchReceiptName), &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.Owner.PID != watcher.cmd.Process.Pid {
			t.Fatal("concurrent watcher changed receipt ownership")
		}
	}
	if mode == "restart" || mode == "restart-twice" || mode == "missing-receipt" || strings.HasPrefix(mode, "changed-") {
		watcher.killed(t)
		if _, err := os.Lstat(filepath.Join(work, "runner.lock")); !os.IsNotExist(err) {
			t.Fatal("cleanup exclusion was not released before interruption")
		}
		testCleanupWatchPublicStatus(t, d, env, root, manifest, work, "ready_to_resume")
		if mode == "missing-receipt" || strings.HasPrefix(mode, "changed-") {
			testCleanupWatchRejectChangedEvidence(t, mode, args, env, root, work)
			assertConsumerAcceptanceKey(t, b, journals[2].Faults.Consumers["customer"].AppID, key, true)
		}
		retry := startCleanupWatchAcceptanceProcess(t, "watcher", args, env...)
		retry.state(t, "owner_terminated_grace_period")
		if mode == "restart-twice" {
			retry.state(t, "cleanup_unresolved")
			retry.killed(t)
			retry = startCleanupWatchAcceptanceProcess(t, "watcher", args, env...)
			retry.state(t, "owner_terminated_grace_period")
		}
		setOutage(false)
		retry.state(t, "cleanup_complete")
		retry.wait(t, 0)
		var receipt testCampaignWatchReceipt
		if err := readTestCampaignJSON(filepath.Join(work, testCampaignWatchReceiptName), &receipt); err != nil {
			t.Fatal(err)
		}
		minimum := 1
		if mode == "restart-twice" {
			minimum = 2
		}
		if !receipt.Complete || receipt.Attempts < 2 || receipt.Progress == nil || len(receipt.PreviousOwners) < minimum || receipt.Owner.PID != retry.cmd.Process.Pid {
			t.Fatal("replacement watcher did not persist recovery progress")
		}
	} else if mode == "restart-in-flight" {
		// Kill the watcher after SQL cancellation commits but before its reply
		// reaches the client. Its runner and handoff exclusion remain on disk.
		killed := make(chan error, 1)
		d.mu.Lock()
		d.crashAfterCommit = func() error {
			err := watcher.cmd.Process.Kill()
			d.releaseCreation()
			killed <- err
			return err
		}
		d.mu.Unlock()
		setOutage(false)
		select {
		case err := <-killed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("watcher did not reach the committed cancellation boundary")
		}
		watcher.verifyKilled(t)
		assertConsumerAcceptanceKey(t, b, journals[2].Faults.Consumers["customer"].AppID, key, false)
		if _, err := os.Stat(filepath.Join(work, "runner.lock")); err != nil {
			t.Fatal("in-flight interruption did not retain runner exclusion", err)
		}
		retry := startCleanupWatchAcceptanceProcess(t, "watcher", args, env...)
		retry.state(t, "owner_terminated_grace_period")
		retry.state(t, "cleanup_complete")
		retry.wait(t, 0)
		var receipt testCampaignWatchReceipt
		if err := readTestCampaignJSON(filepath.Join(work, testCampaignWatchReceiptName), &receipt); err != nil {
			t.Fatal(err)
		}
		if !receipt.Complete || len(receipt.PreviousOwners) != 1 || receipt.Owner.PID != retry.cmd.Process.Pid {
			t.Fatal("in-flight takeover did not persist its successor")
		}

	} else {
		setOutage(false)
		watcher.state(t, "cleanup_complete")
		watcher.wait(t, 0)
	}
	// The SQL oracle and the common portable fixture verify credentials, every
	// generation's sessions/namespace, no provisioning replay and frozen bytes.
	assertConsumerAcceptanceKey(t, b, journals[2].Faults.Consumers["customer"].AppID, key, false)
	testCleanupWatchPublicStatus(t, d, env, root, manifest, work, "cleanup_recorded_complete")
}

func waitCleanupWatchCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("process ownership condition timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertCleanupWatchOutage(t *testing.T, work string) {
	t.Helper()
	// The same persisted observation used by status/recovery must remain unresolved.
	entries, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "fault-history-cleanup-") {
			continue
		}
		var result testCampaignFaultHistory
		body, err := os.ReadFile(filepath.Join(work, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if json.Unmarshal(body, &result) != nil || result.Issue == nil || result.SchemaVersion != 1 {
			continue
		}
		if result.Complete || !result.Issue.Retryable || len(result.Faults) != 3 || len(result.Sessions) != 3 {
			t.Fatal("outage lost unresolved inventory")
		}
		for _, fault := range result.Faults {
			if fault.Consumers["customer"] != "unverified" || fault.Registration != "unverified" {
				t.Fatal("outage certified resource absence")
			}
		}
		for _, session := range result.Sessions {
			if session.Status != "unverified" {
				t.Fatal("outage certified session absence")
			}
		}
		found = true
	}
	if !found {
		t.Fatal("watcher did not persist outage evidence")
	}
}

func testCleanupWatchRejectChangedEvidence(t *testing.T, mode string, args, env []string, root, work string) {
	t.Helper()
	path := filepath.Join(work, testCampaignWatchReceiptName)
	var receipt testCampaignWatchReceipt
	if err := readTestCampaignJSON(path, &receipt); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "changed-progress":
		if receipt.Progress == nil {
			t.Fatal("outage did not save progress receipt")
		}
		path = filepath.Join(work, receipt.Progress.Name)
	case "changed-checkpoint":
		path = filepath.Join(work, "checkpoint.json")
	case "changed-handoff":
		path = filepath.Join(root, "bundle-index.json")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "missing-receipt" {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	} else {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(before, &fields); err != nil {
			t.Fatal(err)
		}
		switch mode {
		case "changed-progress":
			fields["operation_complete"] = json.RawMessage("true")
		case "changed-checkpoint":
			fields["halted"] = json.RawMessage("false")
		case "changed-handoff":
			fields["nonce"] = json.RawMessage(`"changed"`)
		}
		mustCampaignFaultJSON(t, path, fields)
	}
	retryArgs := append([]string{}, args...)
	retryArgs[len(retryArgs)-1] = "2"
	retry := startCleanupWatchAcceptanceProcess(t, "watcher", retryArgs, env...)
	if mode == "missing-receipt" {
		retry.state(t, "manual_inspection_required")
		retry.wait(t, 2)
	} else {
		retry.wait(t, 1)
	}
	// Evidence must stay untouched by the rejected successor.
	if mode == "missing-receipt" {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("missing receipt was silently recreated")
		}
	}
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
}

func testCleanupWatchPublicStatus(t *testing.T, d *consumerAcceptanceDelivery, env []string, root, manifest, work, want string) {
	t.Helper()
	before := campaignFaultFrozenBytes(t, []string{root})
	d.mu.Lock()
	requests := d.requests
	d.mu.Unlock()
	output := filepath.Join(t.TempDir(), "watch-status.json")
	html := filepath.Join(t.TempDir(), "watch-status.html")
	args := []string{"status", "--plan", filepath.Join(root, "artifacts", "plan.json"), "--policy", filepath.Join(root, "artifacts", "policy.yaml"), "--manifest", manifest, "--directory", work, "--handoff", filepath.Join(root, "bundle-index.json"), "--json", output, "--html", html}
	child := startCleanupWatchAcceptanceProcess(t, "status", args, env...)
	child.wait(t, 0)
	var report testCampaignStatusReport
	if err := readTestCampaignJSON(output, &report); err != nil {
		t.Fatal(err)
	}
	status := report.Watcher
	if status == nil || status.State != want || !status.EvidenceVerified || status.Progress == nil || !status.SnapshotStable || !report.SnapshotStable || !report.ReadOnly {
		t.Fatalf("public watcher status mismatch: %+v", status)
	}
	if want == "active" {
		if status.Guard != "held" || status.Owner.Status != "stopped" || status.ResumeReady {
			t.Fatal("status displaced or ignored a protected watcher")
		}
	} else if !status.ResumeReady || !status.BindingVerified {
		t.Fatal("interrupted watcher lost verified resume guidance")
	}
	if want == "cleanup_recorded_complete" {
		if !status.RecordedComplete || !status.Progress.RecordedComplete {
			t.Fatal("recorded cleanup completion was lost")
		}
	} else if status.RecordedComplete || status.Progress.RecordedComplete {
		t.Fatal("outage certified cleanup completion")
	}
	if report.Counts["completed"] != 0 || report.Counts["recovered"] != 0 {
		t.Fatal("watcher status forged test execution")
	}
	body, err := os.ReadFile(html)
	if err != nil || !bytes.Contains(body, []byte("Cleanup watcher")) {
		t.Fatal("offline HTML lost watcher status")
	}
	d.mu.Lock()
	after := d.requests
	d.mu.Unlock()
	if after != requests {
		t.Fatal("offline status made API requests", after-requests)
	}
	assertCampaignFaultFrozen(t, before, []string{root})
}
