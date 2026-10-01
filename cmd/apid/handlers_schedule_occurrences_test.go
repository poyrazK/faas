package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestListJobScheduleOccurrences(t *testing.T) {
	e := setup(t, api.PlanHobby)
	job, err := e.store.JobCreateScheduledIfUnderQuota(context.Background(), state.Job{
		AccountID: e.acct.ID, Name: "occurrence-history", Kind: "recurring",
		ImageRef: "ghcr.io/example/worker:v1", Command: []string{"/app/run"},
		RAMMB: 256, TaskTimeoutS: 60, MaxParallelism: 1, RetryMax: 1,
		CronSchedule: "* * * * *", CronTimezone: "UTC",
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "skip", MissedRuns: "skip"},
	}, api.JobMaxPerAccount[api.PlanHobby.PlanIndex()])
	if err != nil {
		t.Fatalf("create scheduled job: %v", err)
	}
	first := job.CreatedAt.Add(time.Minute).UTC()
	run, created, err := e.store.JobRunCreateScheduledOccurrence(context.Background(), job.ID, job.CronSchedule,
		job.CronTimezone, nil, first, state.JobScheduledOccurrenceOptions{ScheduledFor: first, ScheduleRevision: job.ScheduleRevision})
	if err != nil || !created {
		t.Fatalf("create first occurrence run=%+v created=%t err=%v", run, created, err)
	}
	second := first.Add(time.Minute)
	if _, created, err := e.store.JobRunCreateScheduledOccurrence(context.Background(), job.ID, job.CronSchedule,
		job.CronTimezone, &first, second, state.JobScheduledOccurrenceOptions{ScheduledFor: second, ScheduleRevision: job.ScheduleRevision}); err != nil || created {
		t.Fatalf("create overlapping occurrence created=%t err=%v; want durable skip without a run", created, err)
	}

	page := e.do(t, http.MethodGet, "/v1/jobs/occurrence-history/occurrences?limit=1", nil, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("GET occurrence history = %d: %s", page.Code, page.Body.String())
	}
	var response api.ListScheduleOccurrencesResponse
	if err := json.Unmarshal(page.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode occurrence page: %v", err)
	}
	if len(response.Occurrences) != 1 || response.Occurrences[0].Status != "skipped_overlap" || response.Occurrences[0].Reason == "" {
		t.Fatalf("latest occurrence = %+v; want explained skipped_overlap", response.Occurrences)
	}
	if response.NextBefore == "" {
		t.Fatalf("next_before missing for another page: %+v", response)
	}
	older := e.do(t, http.MethodGet, "/v1/jobs/occurrence-history/occurrences?before="+response.NextBefore, nil, nil)
	if older.Code != http.StatusOK {
		t.Fatalf("GET older occurrence = %d: %s", older.Code, older.Body.String())
	}
}
