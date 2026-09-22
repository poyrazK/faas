package main

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDelayedTaskScheduleAcceptsRelativeDelay(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	got, problem := delayedTaskSchedule(now, api.DelayedTaskRequest{DelaySeconds: 30 * 60})
	if problem != nil {
		t.Fatalf("problem = %+v", problem)
	}
	want := now.Add(30 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("scheduled = %s, want %s", got, want)
	}
}

func TestDelayedTaskScheduleRejectsAmbiguousAndOutOfWindow(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	cases := []api.DelayedTaskRequest{
		{},
		{ScheduledAt: now.Add(time.Hour), DelaySeconds: 60},
		{DelaySeconds: int64(api.MaxDelayedTaskDelaySeconds) + 1},
		{ScheduledAt: now.Add((365*24*time.Hour + time.Second))},
	}
	for _, req := range cases {
		if _, problem := delayedTaskSchedule(now, req); problem == nil || problem.Code != "invalid_scheduled_at" {
			t.Errorf("request %+v problem = %+v, want invalid_scheduled_at", req, problem)
		}
	}
}
