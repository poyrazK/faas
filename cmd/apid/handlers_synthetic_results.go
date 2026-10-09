package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// syntheticCheckResults summarises a check's runs (ADR-748). A store without
// run history, or a failed read, yields nil so the definition still renders.
func (s *server) syntheticCheckResults(ctx context.Context, checkID string, now time.Time) *api.SyntheticCheckResults {
	runs, ok := s.store.(state.SyntheticRunStore)
	if !ok {
		return nil
	}
	day, err := runs.SyntheticCheckStats(ctx, checkID, now.Add(-24*time.Hour))
	if err != nil {
		return nil
	}
	week, err := runs.SyntheticCheckStats(ctx, checkID, now.Add(-api.SyntheticCheckRunRetentionDays*24*time.Hour))
	if err != nil {
		return nil
	}
	recent, err := runs.ListSyntheticCheckRuns(ctx, checkID, api.SyntheticCheckRecentRuns)
	if err != nil {
		return nil
	}
	out := &api.SyntheticCheckResults{
		Uptime24hPct: uptimePct(day), Uptime7dPct: uptimePct(week),
		Runs24h: day.Runs, P95LatencyMS24h: day.P95LatencyMS,
		Recent: make([]api.SyntheticCheckRun, 0, len(recent)),
	}
	for _, run := range recent {
		out.Recent = append(out.Recent, api.SyntheticCheckRun{
			StartedAt: run.StartedAt.UTC().Format(time.RFC3339), OK: run.OK, StatusCode: run.StatusCode,
			LatencyMS: run.LatencyMS, ErrorClass: run.ErrorClass,
		})
	}
	return out
}

func uptimePct(st state.SyntheticCheckStats) *float64 {
	if st.Runs == 0 {
		return nil
	}
	v := float64(st.OKRuns) / float64(st.Runs) * 100
	return &v
}
