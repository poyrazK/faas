package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCampaignCleanupWatchOwnership(t *testing.T) {
	// adr: 984 — neither elapsed time nor missing ownership evidence authorizes cleanup.
	for _, mode := range []string{"active", "unknown", "missing", "changed", "recovery_blocked", "outage", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads, recovered, mutations := 0, 0, 0
			if mode == "cancelled" {
				cancel()
			}
			inspect := func() (string, bool, error) {
				reads++
				switch mode {
				case "active":
					return "owner", false, nil
				case "unknown":
					return "", false, errors.New("unverifiable")
				case "missing":
					return "", false, nil
				case "changed":
					return time.Now().String(), true, nil
				default:
					return "dead", true, nil
				}
			}
			code := watchTestCampaignCleanup(ctx, time.Millisecond, time.Millisecond, 5, inspect,
				func() bool { recovered++; return mode != "recovery_blocked" },
				func() bool { mutations++; return mutations == 2 }, func(string, int) error { return nil })
			if mode == "outage" {
				if code != 0 || recovered != 1 || mutations != 2 {
					t.Fatalf("code=%d recoveries=%d mutations=%d", code, recovered, mutations)
				}
			} else if code != 2 || mutations != 0 {
				t.Fatalf("unsafe cleanup: code=%d mutations=%d", code, mutations)
			}
			if mode == "cancelled" && reads != 0 {
				t.Fatal("cancelled watcher inspected owner")
			}
		})
	}
}

func TestCampaignCleanupWatchExhaustion(t *testing.T) {
	// adr: 984 — outages remain unresolved after a finite attempt budget; no false completion.
	calls := 0
	states := []string{}
	code := watchTestCampaignCleanup(context.Background(), time.Millisecond, 0, 3,
		func() (string, bool, error) { return "dead", true, nil }, func() bool { return true },
		func() bool { calls++; return false }, func(state string, _ int) error { states = append(states, state); return nil })
	if code != 2 || calls != 3 || states[len(states)-1] != "manual_inspection_required" {
		t.Fatalf("code=%d calls=%d states=%v", code, calls, states)
	}
}

func TestCampaignCleanupWatchCancellationAfterRecovery(t *testing.T) {
	// adr: 984 — stopping after lock recovery must not start a new cleanup operation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mutations := 0
	code := watchTestCampaignCleanup(ctx, time.Millisecond, 0, 1,
		func() (string, bool, error) { return "dead", true, nil },
		func() bool { cancel(); return true },
		func() bool { mutations++; return true }, func(string, int) error { return nil })
	if code != 2 || mutations != 0 {
		t.Fatalf("code=%d mutations=%d", code, mutations)
	}
}

func newCampaignWatchReceiptFixture(t *testing.T) (*testCampaignWatchRecovery, campaignFaultHistoryFixture) {
	t.Helper()
	f := newCampaignFaultHistoryFixture(t)
	plan := filepath.Join(f.root, "plan.json")
	mustCampaignFaultJSON(t, plan, f.plan)
	t.Setenv("FAAS_API", "http://watcher-acceptance.invalid")
	t.Setenv("FAAS_TOKEN", "watcher-test-token")
	w, err := newTestCampaignWatchRecovery(testCampaignWatchPaths{Plan: plan, Policy: filepath.Join(f.root, "policy.yaml"), Manifest: filepath.Join(f.root, "manifest.yaml"), Directory: f.dirs[2]})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.recordInitial(); err != nil {
		t.Fatal(err)
	}
	return w, f
}

func TestCampaignCleanupWatchReceiptGuards(t *testing.T) {
	// adr: 984 — a durable receipt cannot waive changed evidence, active owners or unknown workers.
	if runtime.GOOS != "linux" {
		t.Skip("watcher recovery requires Linux process identity")
	}
	for _, mode := range []string{"valid", "alive_owner", "unknown_owner", "checkpoint", "journal", "manifest", "endpoint", "receipt", "missing_progress", "changed_progress", "foreign_runner", "owner_limit"} {
		t.Run(mode, func(t *testing.T) {
			w, f := newCampaignWatchReceiptFixture(t)
			switch mode {
			case "valid":
				if err := w.validate(false); err != nil {
					t.Fatal(err)
				}
				if err := w.saveProgress(false); err != nil {
					t.Fatal(err)
				}
				if w.Receipt.Attempts != 1 || w.Receipt.Complete {
					t.Fatal("invalid durable progress")
				}
				return
			case "alive_owner":
				if err := w.validate(true); err == nil {
					t.Fatal("active owner was accepted")
				}
				return
			case "unknown_owner":
				w.Receipt.Owner.Start = "invalid"
				if err := w.persist(); err != nil {
					t.Fatal(err)
				}
			case "checkpoint":
				cp := f.cp
				cp.Halted = false
				mustCampaignFaultJSON(t, filepath.Join(w.Paths.Directory, "checkpoint.json"), cp)
			case "journal":
				saved, err := readTestCampaignFaultJournals(w.Paths.Directory, "a", f.scenarios["checkout"])
				if err != nil {
					t.Fatal(err)
				}
				saved[0].Faults.Consumers["customer"] = testCampaignFaultConsumer{}
				mustCampaignFaultJSON(t, filepath.Join(testCampaignJournalDirectory(w.Paths.Directory, "a"), saved[0].RunID+".json"), saved[0])
			case "manifest":
				if err := os.WriteFile(w.Paths.Manifest, []byte("invalid: true\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "endpoint":
				w.EndpointSHA256 = "changed"
			case "receipt":
				changed := *w.Receipt
				changed.Attempts++
				mustCampaignFaultJSON(t, filepath.Join(w.Paths.Directory, testCampaignWatchReceiptName), changed)
			case "missing_progress", "changed_progress":
				progress := testCampaignFaultHistory{SchemaVersion: 1, Executed: true, PlanSHA256: testCampaignJSONHash(f.plan), SnapshotSHA256: w.Receipt.InventorySHA256}
				name := "fault-history-cleanup-test.json"
				mustCampaignFaultJSON(t, filepath.Join(w.Paths.Directory, name), progress)
				w.Receipt.Progress = &testCampaignWatchProgress{Name: name, SHA256: testCampaignJSONHash(progress)}
				if err := w.persist(); err != nil {
					t.Fatal(err)
				}
				if mode == "missing_progress" {
					if err := os.Remove(filepath.Join(w.Paths.Directory, name)); err != nil {
						t.Fatal(err)
					}
				} else {
					progress.Complete = true
					mustCampaignFaultJSON(t, filepath.Join(w.Paths.Directory, name), progress)
				}
			case "foreign_runner":
				release, err := acquireTestCampaignRunnerLock(w.Paths.Directory, testCampaignJSONHash(f.plan))
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				if _, err := w.staleRunner(); err == nil {
					t.Fatal("active runner was accepted")
				}
				return
			case "owner_limit":
				w.Receipt.PreviousOwners = make([]testCampaignProcessIdentity, testCampaignWatchOwnerLimit)
				if err := w.persist(); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.validate(false); err == nil {
				t.Fatal("changed receipt evidence was accepted", mode)
			}
		})
	}
}
