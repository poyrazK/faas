//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func campaignStatusWatchProgress(t *testing.T, w *testCampaignWatchRecovery, f campaignFaultHistoryFixture, complete bool) {
	t.Helper()
	progress := testCampaignFaultHistory{SchemaVersion: 1, Executed: true, PlanSHA256: testCampaignJSONHash(f.plan), SnapshotSHA256: w.Receipt.InventorySHA256, Complete: complete,
		Faults:   []testCampaignFaultObservation{{RunID: strings.Repeat("1", 32), Registration: "unverified", Consumers: map[string]string{"customer": "unverified"}}},
		Sessions: []testCampaignCleanupAction{{RunID: strings.Repeat("1", 32), Project: "checkout", Status: "unverified"}},
		Issue:    &testCampaignRecoveryIssue{Resource: "fault-history", Operation: "cleanup", Reason: "api_unavailable", Attempts: 3, Retryable: true}}
	if complete {
		progress.Faults[0].Registration = "absent"
		progress.Faults[0].Consumers["customer"] = "revoked"
		progress.Sessions[0].Status = "absent"
		progress.Issue = nil
	}
	mustCampaignFaultJSON(t, filepath.Join(w.Paths.Directory, "fault-history-cleanup-status.json"), progress)
	if err := w.saveProgress(complete); err != nil {
		t.Fatal(err)
	}
}

func TestCampaignStatusWatcherStates(t *testing.T) {
	// adr: 984 — status is an offline observation; historical cleanup cannot certify test execution.
	for _, mode := range []string{"active", "takeover_guard", "interrupted", "recorded_complete", "offline", "missing", "malformed", "checkpoint_changed", "progress_changed", "owner_unknown", "worker_foreign"} {
		t.Run(mode, func(t *testing.T) {
			w, f := newCampaignWatchReceiptFixture(t)
			campaignStatusWatchProgress(t, w, f, mode == "recorded_complete")
			owner, identity := campaignWatchReceiptProcess(t)
			killCampaignWatchReceiptProcess(t, owner)
			if mode != "active" {
				w.Receipt.Owner = identity
				if err := w.persist(); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "takeover_guard":
				release, err := acquireTestCampaignRecoveryGuard(filepath.Join(w.Paths.Directory, "cleanup-watch.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			case "offline":
				t.Setenv("FAAS_TOKEN", "")
			case "missing":
				if err := os.Remove(filepath.Join(w.Paths.Directory, testCampaignWatchReceiptName)); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(filepath.Join(w.Paths.Directory, testCampaignWatchReceiptName), []byte(`{"secret":"must-not-be-exported"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "checkpoint_changed":
				changed := f.cp
				changed.Halted = false
				mustCampaignFaultJSON(t, filepath.Join(w.Paths.Directory, "checkpoint.json"), changed)
			case "progress_changed":
				if err := os.WriteFile(filepath.Join(w.Paths.Directory, w.Receipt.Progress.Name), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "owner_unknown":
				w.Receipt.Owner.Start = "invalid"
				if err := w.persist(); err != nil {
					t.Fatal(err)
				}
			case "worker_foreign":
				release, err := acquireTestCampaignRunnerLock(w.Paths.Directory, testCampaignJSONHash(f.plan))
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			before := campaignFaultFrozenBytes(t, []string{f.root})
			snapshot := inspectTestCampaignStatusWatcher(w.Paths, f.plan, &f.cp, f.scenarios, nil)
			report, err := buildTestCampaignStatus(f.plan, &f.cp, w.Paths.Directory, "", f.policy, f.scenarios, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			counts, passed := testCampaignJSONHash(report.Counts), report.PolicyPassed
			applyTestCampaignStatusWatcherSnapshot(&report, snapshot)
			status := report.Watcher
			switch mode {
			case "active", "takeover_guard":
				if status.State != "active" || status.ResumeReady {
					t.Fatalf("active watcher was not protected: %+v", status)
				}
			case "interrupted", "recorded_complete":
				if !status.ResumeReady || !status.EvidenceVerified || !status.BindingVerified || status.Attempts != 1 || status.Progress == nil || status.Progress.RecordedComplete != (mode == "recorded_complete") {
					t.Fatalf("valid receipt not inspectable: %+v", status)
				}
				if !strings.Contains(status.Command, "cleanup-watch") || !strings.Contains(status.Command, "--execute") {
					t.Fatal("resume command missing")
				}
			default:
				if status.ResumeReady || status.State != "manual_inspection_required" || len(status.Blockers) == 0 {
					t.Fatalf("unsafe status: %+v", status)
				}
			}
			if mode == "offline" && (!status.EvidenceVerified || status.Progress == nil || status.BindingVerified) {
				t.Fatal("offline status lost local evidence or invented resume binding")
			}
			if counts != testCampaignJSONHash(report.Counts) || passed != report.PolicyPassed || report.Counts["completed"] != 0 {
				t.Fatal("cleanup forged execution evidence")
			}
			body, err := renderTestCampaignStatusHTML(report)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(body, []byte("Cleanup watcher")) || bytes.Contains(body, []byte("must-not-be-exported")) {
				t.Fatal("watcher HTML missing or invalid receipt leaked")
			}
			assertCampaignFaultFrozen(t, before, []string{f.root})
		})
	}
}

func TestCampaignStatusWatcherSnapshotAndGuard(t *testing.T) {
	// adr: 984 — inspection must neither acquire exclusion nor trust evidence that changed while read.
	w, _ := newCampaignWatchReceiptFixture(t)
	guardPath := filepath.Join(w.Paths.Directory, "cleanup-watch.lock")
	if state, _ := inspectTestCampaignWatchGuard(guardPath); state != "absent" {
		t.Fatal(state)
	}
	if _, err := os.Lstat(guardPath); !os.IsNotExist(err) {
		t.Fatal("status created a guard")
	}
	release, err := acquireTestCampaignRecoveryGuard(guardPath)
	if err != nil {
		t.Fatal(err)
	}
	if state, pid := inspectTestCampaignWatchGuard(guardPath); state != "held" || pid != os.Getpid() {
		t.Fatalf("held guard: %s %d", state, pid)
	}
	release()
	if state, _ := inspectTestCampaignWatchGuard(guardPath); state != "available" {
		t.Fatal(state)
	}
	for _, mode := range []string{"receipt", "journal", "guard"} {
		t.Run(mode, func(t *testing.T) {
			w, f := newCampaignWatchReceiptFixture(t)
			snapshot := inspectTestCampaignStatusWatcher(w.Paths, f.plan, &f.cp, f.scenarios, nil)
			var unlock func()
			switch mode {
			case "receipt":
				w.Receipt.Attempts++
				if err := w.persist(); err != nil {
					t.Fatal(err)
				}
			case "journal":
				entries, err := readTestCampaignFaultJournals(w.Paths.Directory, "a", f.scenarios["checkout"])
				if err != nil {
					t.Fatal(err)
				}
				entries[0].Faults.Registration = "changed"
				mustCampaignFaultJSON(t, filepath.Join(testCampaignJournalDirectory(w.Paths.Directory, "a"), entries[0].RunID+".json"), entries[0])
			case "guard":
				var err error
				unlock, err = acquireTestCampaignRecoveryGuard(filepath.Join(w.Paths.Directory, "cleanup-watch.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
			}
			report := testCampaignStatusReport{SnapshotStable: true}
			applyTestCampaignStatusWatcherSnapshot(&report, snapshot)
			if report.SnapshotStable || report.Watcher.SnapshotStable || report.Watcher.ResumeReady || report.Watcher.Progress != nil || report.Watcher.Command != "" {
				t.Fatal("changed snapshot retained readiness")
			}
		})
	}
}

func TestCampaignStatusWatcherCLIAndAliases(t *testing.T) {
	// adr: 984 — JSON and HTML exports preserve watcher evidence, including through hard-link aliases.
	w, f := newCampaignWatchReceiptFixture(t)
	campaignStatusWatchProgress(t, w, f, false)
	before := campaignFaultFrozenBytes(t, []string{f.root})
	output := filepath.Join(t.TempDir(), "status.json")
	html := filepath.Join(t.TempDir(), "status.html")
	args := []string{"--plan", w.Paths.Plan, "--policy", w.Paths.Policy, "--manifest", w.Paths.Manifest, "--directory", w.Paths.Directory}
	if code := cmdTestCampaignStatus(append(append([]string{}, args...), "--json", output, "--html", html)); code != 0 {
		t.Fatal(code)
	}
	var report testCampaignStatusReport
	if err := readTestCampaignJSON(output, &report); err != nil {
		t.Fatal(err)
	}
	if report.Watcher == nil || report.Watcher.Attempts != 1 || report.Watcher.State != "active" || !report.ReadOnly || !report.Informational {
		t.Fatal("CLI watcher status missing")
	}
	for _, input := range []string{filepath.Join(w.Paths.Directory, testCampaignWatchReceiptName), filepath.Join(w.Paths.Directory, w.Receipt.Progress.Name)} {
		alias := filepath.Join(t.TempDir(), "alias.json")
		if err := os.Link(input, alias); err != nil {
			t.Fatal(err)
		}
		if code := cmdTestCampaignStatus(append(append([]string{}, args...), "--json", alias)); code == 0 {
			t.Fatal("status accepted an evidence hard link")
		}
	}
	status := &testCampaignStatusWatcher{}
	status.command(testCampaignWatchPaths{Plan: "/tmp/a'$(cmd).json", Directory: "/private/run space", Policy: "p.yaml", Manifest: "m.yaml"}, "cleanup-watch", true)
	if !strings.Contains(status.Command, quoteLogCommandArg("/tmp/a'$(cmd).json")) {
		t.Fatal("command arguments are not safely quoted")
	}
	report.Watcher = &testCampaignStatusWatcher{State: "<script>alert(1)</script>", NextAction: "<img src=x onerror=alert(1)>", Command: status.Command}
	body, err := renderTestCampaignStatusHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("<script>")) || bytes.Contains(body, []byte("<img src=x")) {
		t.Fatal("HTML executed watcher evidence")
	}
	assertCampaignFaultFrozen(t, before, []string{f.root})
}
