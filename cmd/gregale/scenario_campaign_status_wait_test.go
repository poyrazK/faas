package main

import (
	"context"
	"testing"
	"time"
)

func TestCampaignStatusWaitClean(t *testing.T) {
	// adr: 984 — polling rechecks fresh observations, stops on evidence changes, and shares a bounded deadline.
	for _, mode := range []string{"clean", "eventual", "outage", "present_deadline", "unknown_deadline", "changed", "blocked", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			report := testCampaignStatusReport{SnapshotStable: true}
			calls := 0
			verify := func(ctx context.Context) {
				calls++
				now := time.Now()
				live := &testCampaignStatusLiveReport{SnapshotStable: true, AllVerified: true, State: "verified_resources_present", Resources: []testCampaignStatusLiveResource{{Kind: "consumer", State: "active", VerifiedAt: &now}}}
				report.LiveVerification = live
				switch mode {
				case "clean", "eventual", "outage":
					if mode == "outage" && calls == 1 {
						live.AllVerified = false
						live.State = "unverified"
						live.Resources[0].VerifiedAt = nil
					} else if mode == "clean" || calls >= 2 {
						live.State = "verified_clean"
						live.ResourcesClean = true
						live.Resources[0].State = "cancelled"
					}
				case "unknown_deadline":
					live.AllVerified = false
					live.State = "unverified"
				case "changed":
					report.SnapshotStable = false
				case "blocked":
					live.State = "blocked"
				case "cancelled":
					cancel()
				}
			}
			waitTestCampaignStatusClean(ctx, &report, time.Millisecond, verify)
			code, _ := testCampaignStatusCleanGate(report)
			want := 5
			if mode == "clean" || mode == "eventual" || mode == "outage" {
				want = 0
			}
			if mode == "present_deadline" {
				want = 4
			}
			if code != want {
				t.Fatalf("exit %d want %d: %+v", code, want, report.CleanWait)
			}
			if report.CleanWait.Attempts != calls || calls == 0 {
				t.Fatal("attempt count lost")
			}
			if mode == "changed" && (calls != 1 || report.CleanWait.Outcome != "evidence_changed") {
				t.Fatal("retried changed evidence")
			}
			if mode == "blocked" && calls != 1 {
				t.Fatal("retried blocked inventory")
			}
			if (mode == "eventual" || mode == "outage") && calls != 2 {
				t.Fatal("did not stop at first clean observation")
			}
		})
	}
	for _, flags := range [][]string{{"--timeout", "1s"}, {"--wait-clean", "--timeout", "0s"}, {"--wait-clean", "--interval", "0s"}, {"--wait-clean", "--timeout", "1s", "--interval", "2s"}} {
		if code := cmdTestCampaignStatus(append([]string{"--plan", "unused", "--policy", "unused", "--directory", "unused"}, flags...)); code == 0 {
			t.Fatal("invalid wait accepted", flags)
		}
	}
}
