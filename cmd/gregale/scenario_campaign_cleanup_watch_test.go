package main

import (
	"context"
	"errors"
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
