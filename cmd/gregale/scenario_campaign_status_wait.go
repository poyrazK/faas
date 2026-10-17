package main

import (
	"context"
	"time"
)

type testCampaignStatusCleanWait struct {
	Attempts            int    `json:"attempts"`
	ElapsedMilliseconds int64  `json:"elapsed_ms"`
	Outcome             string `json:"outcome"`
}

func waitTestCampaignStatusClean(ctx context.Context, report *testCampaignStatusReport, interval time.Duration, verify func(context.Context)) {
	started := time.Now()
	result := &testCampaignStatusCleanWait{}
	report.CleanWait = result
	defer func() {
		result.ElapsedMilliseconds = time.Since(started).Milliseconds()
		if ctx.Err() == context.Canceled {
			result.Outcome = "cancelled"
		}
	}()
	for {
		if ctx.Err() != nil {
			result.Outcome = "deadline_exceeded"
			return
		}
		verify(ctx)
		result.Attempts++
		if !report.SnapshotStable || (report.LiveVerification != nil && !report.LiveVerification.SnapshotStable) {
			result.Outcome = "evidence_changed"
			return
		}
		if ctx.Err() != nil {
			result.Outcome = "deadline_exceeded"
			return
		}
		snapshot := *report
		snapshot.CleanWait = nil
		code, _ := testCampaignStatusCleanGate(snapshot)
		if code == 0 {
			result.Outcome = "clean"
			return
		}
		if report.LiveVerification == nil || report.LiveVerification.State == "blocked" {
			result.Outcome = "blocked"
			return
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Outcome = "deadline_exceeded"
			return
		case <-timer.C:
		}
	}
}
