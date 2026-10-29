package main_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// crons info must show that a cron's recent runs failed; the row alone says
// "enabled" while every run 404s (H10-70).
func TestRenderCronRecentRunsReportsFailures(t *testing.T) {
	at := time.Date(2026, 10, 14, 18, 50, 1, 0, time.UTC)
	failed := api.CronRun{Outcome: api.CronRunFailed, StartedAt: at, Error: "invoke: application returned HTTP 404"}
	var out bytes.Buffer
	main.RenderCronRecentRuns(&out, "c1", []api.CronRun{failed, failed, {Outcome: api.CronRunSuccess, StartedAt: at.Add(-10 * time.Minute)}})
	got := out.String()
	for _, want := range []string{"last run: failed at 2026-10-14T18:50:01Z — invoke: application returned HTTP 404", "2 of the last 3 runs did not succeed", "gregale cron runs c1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q lacks %q", got, want)
		}
	}

	out.Reset()
	main.RenderCronRecentRuns(&out, "c1", []api.CronRun{{Outcome: api.CronRunSuccess, StartedAt: at}})
	if got := out.String(); strings.Contains(got, "did not succeed") || !strings.Contains(got, "last run: success") {
		t.Fatalf("healthy cron output = %q", got)
	}
	out.Reset()
	main.RenderCronRecentRuns(&out, "c1", nil)
	if out.Len() != 0 {
		t.Fatalf("a never-run cron printed %q", out.String())
	}
}
