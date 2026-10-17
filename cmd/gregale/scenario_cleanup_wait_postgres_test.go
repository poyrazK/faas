//go:build !no_pg && !windows

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestScenarioConsumerPostgresWaitClean(t *testing.T) {
	// adr: 984 — a real CLI gate observes watcher cleanup without creating execution evidence or cleanup mutations.
	if runtime.GOOS != "linux" {
		t.Skip("process identity acceptance requires Linux")
	}
	for _, mode := range []string{"present-to-clean", "api-unavailable", "database-unavailable", "restart", "deadline", "changed-evidence"} {
		t.Run(mode, func(t *testing.T) { testConsumerPostgresPortableRecovery(t, false, false, "watch:wait-"+mode) })
	}
}

func testCleanupWatchWaitClean(t *testing.T, mode string, d *consumerAcceptanceDelivery, env, watcherArgs []string, root, manifest, work string, watcher *cleanupWatchAcceptanceProcess, setOutage func(bool)) {
	t.Helper()
	// The caller has stopped the watcher between unresolved cleanup attempts.
	// Hold it stopped while proving the status process itself performs no writes.
	outage := strings.HasSuffix(mode, "unavailable")
	if !outage {
		setOutage(false)
	}
	before := campaignFaultFrozenBytes(t, []string{root})
	d.mu.Lock()
	mutations := d.mutations
	reads := map[string]int{}
	for path, count := range d.completedReads {
		reads[path] = count
	}
	d.mu.Unlock()
	output, html := filepath.Join(t.TempDir(), "wait.json"), filepath.Join(t.TempDir(), "wait.html")
	timeout := "30s"
	if mode == "database-unavailable" {
		timeout = "60s"
	}
	if mode == "deadline" {
		timeout = "2s"
	}
	args := []string{"status", "--plan", filepath.Join(root, "artifacts", "plan.json"), "--policy", filepath.Join(root, "artifacts", "policy.yaml"), "--manifest", manifest, "--directory", work, "--handoff", filepath.Join(root, "bundle-index.json"), "--wait-clean", "--timeout", timeout, "--interval", "1s", "--json", output, "--html", html}
	// Longer spacing gives the mutating watcher time to finish before the next
	// read-only snapshot; outages still use all production read retry behavior.
	if mode != "deadline" && mode != "changed-evidence" {
		args[len(args)-5] = "10s"
	}
	child := startCleanupWatchAcceptanceProcess(t, "status", args, env...)
	waitCleanupWatchCondition(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		observed := 0
		for path, count := range d.completedReads {
			if count > reads[path] {
				observed++
			}
		}
		// A database outage can consume the per-attempt budget before all
		// resources are visited. One completed failed read proves the outage;
		// the quiet window below waits for that bounded attempt to finish.
		if mode == "database-unavailable" {
			return observed >= 1
		}
		return observed == 9
	})
	if mode == "deadline" {
		child.wait(t, testCampaignStatusResourcesPresent)
		assertCampaignFaultFrozen(t, before, []string{root})
	} else if mode == "changed-evidence" {
		path := filepath.Join(root, "artifacts", "plan.json")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// Semantic bindings remain valid, but the immutable input bytes changed
		// after polling began. The gate must stop instead of adopting new evidence.
		if err := os.WriteFile(path, append(append([]byte{}, original...), '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		child.wait(t, testCampaignStatusVerificationIncomplete)
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		assertCampaignFaultFrozen(t, before, []string{root})
	} else {
		// Wait until the first attempt's full resource inventory has been read.
		// The watcher stays stopped through a quiet window so it cannot race the
		// final local snapshot checks of that first attempt.
		quiet := 2100 * time.Millisecond
		budget := 10 * time.Second
		if mode == "database-unavailable" {
			quiet = 7 * time.Second
			budget = 45 * time.Second
		}
		previous := 0
		quietSince := time.Now()
		waitCleanupWatchCondition(t, func() bool {
			d.mu.Lock()
			current := d.requests
			d.mu.Unlock()
			if current != previous {
				previous = current
				quietSince = time.Now()
			}
			return time.Since(quietSince) >= quiet
		}, budget)
		assertCampaignFaultFrozen(t, before, []string{root})
	}
	d.mu.Lock()
	changed := d.mutations
	d.mu.Unlock()
	if changed != mutations {
		t.Fatal("wait status attempted resource mutations while watcher was stopped")
	}
	if mode == "restart" {
		watcher.killed(t)
		watcher = startCleanupWatchAcceptanceProcess(t, "watcher", watcherArgs, env...)
		watcher.state(t, "owner_terminated_grace_period")
	} else if err := watcher.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	setOutage(false)
	watcher.state(t, "cleanup_complete")
	watcher.wait(t, 0)
	if mode != "deadline" && mode != "changed-evidence" {
		child.wait(t, 0)
	}
	var report testCampaignStatusReport
	if err := readTestCampaignJSON(output, &report); err != nil {
		t.Fatal(err)
	}
	stdoutReports := 0
	for raw := range child.events {
		var emitted testCampaignStatusReport
		if err := json.Unmarshal(raw, &emitted); err != nil {
			t.Fatal(err)
		}
		if testCampaignJSONHash(emitted) != testCampaignJSONHash(report) {
			t.Fatal("stdout and exported report differ")
		}
		stdoutReports++
	}
	if stdoutReports != 1 {
		t.Fatal("wait emitted intermediate or missing stdout reports", stdoutReports)
	}
	wait, live := report.CleanWait, report.LiveVerification
	if wait == nil || wait.Attempts < 1 || wait.ElapsedMilliseconds < 0 || live == nil || len(live.Resources) != 9 || !report.ReadOnly || report.Counts["completed"] != 0 || report.Counts["recovered"] != 0 {
		t.Fatal("wait report lost inventory or forged execution")
	}
	switch mode {
	case "deadline":
		if wait.Outcome != "deadline_exceeded" || !live.AllVerified || live.ResourcesClean || live.State != "verified_resources_present" || wait.ElapsedMilliseconds < 1900 {
			t.Fatal("deadline did not retain verified present resources", wait, live)
		}
	case "changed-evidence":
		if wait.Outcome != "evidence_changed" || report.SnapshotStable || live.ResourcesClean || live.AllVerified {
			t.Fatal("changed evidence retained cleanup proof", wait, live)
		}
	default:
		if wait.Outcome != "clean" || wait.Attempts < 2 || !live.AllVerified || !live.ResourcesClean || !report.SnapshotStable {
			t.Fatal("wait did not follow real cleanup to completion", wait, live)
		}
		for _, resource := range live.Resources {
			if resource.CheckedAt == nil || resource.VerifiedAt == nil {
				t.Fatal("missing fresh verification timestamp")
			}
		}
	}
	body, err := os.ReadFile(html)
	if err != nil || !bytes.Contains(body, []byte("Wait for clean resources")) || !bytes.Contains(body, []byte(wait.Outcome)) {
		t.Fatal("wait HTML omitted outcome", err)
	}
}
