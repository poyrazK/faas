package main

import (
	"testing"
	"time"
)

func TestCampaignStatusCleanGate(t *testing.T) {
	// adr: 984 — CI cleanup proof must fail closed independently of policy and execution results.
	for _, tc := range []struct {
		name   string
		mutate func(*testCampaignStatusReport)
		want   int
	}{
		{"clean", func(r *testCampaignStatusReport) {}, 0},
		{"missing", func(r *testCampaignStatusReport) { r.LiveVerification = nil }, 5},
		{"empty", func(r *testCampaignStatusReport) { r.LiveVerification.Resources = nil }, 5},
		{"changed", func(r *testCampaignStatusReport) { r.SnapshotStable = false }, 5},
		{"unknown", func(r *testCampaignStatusReport) { r.LiveVerification.Resources[0].State = "unverified" }, 5},
		{"missing_timestamp", func(r *testCampaignStatusReport) { r.LiveVerification.Resources[0].VerifiedAt = nil }, 5},
		{"incomplete", func(r *testCampaignStatusReport) { r.LiveVerification.AllVerified = false }, 5},
		{"pending", func(r *testCampaignStatusReport) {
			r.LiveVerification.Resources[0].State = "pending"
			r.LiveVerification.ResourcesClean = false
			r.LiveVerification.State = "verified_resources_present"
		}, 4},
		{"inconsistent", func(r *testCampaignStatusReport) { r.LiveVerification.ResourcesClean = false }, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			r := testCampaignStatusReport{SnapshotStable: true, LiveVerification: &testCampaignStatusLiveReport{SnapshotStable: true, AllVerified: true, ResourcesClean: true, State: "verified_clean", Resources: []testCampaignStatusLiveResource{{Kind: "consumer", State: "cancelled", VerifiedAt: &now}}}}
			tc.mutate(&r)
			if got, summary := testCampaignStatusCleanGate(r); got != tc.want || summary == "" {
				t.Fatalf("exit %d want %d: %s", got, tc.want, summary)
			}
		})
	}
	if code := cmdTestCampaignStatus([]string{"--plan", "unused", "--policy", "unused", "--require-clean"}); code == 0 {
		t.Fatal("gate accepted without live verification")
	}
}
