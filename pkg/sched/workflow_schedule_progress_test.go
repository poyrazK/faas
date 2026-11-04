package sched_test

// adr: 487

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type slowPageSchedules struct {
	MemStore   *state.MemStore
	candidates []state.WorkflowScheduleCandidate
	seen       map[string]int
}

func (s *slowPageSchedules) ListWorkflowScheduleCandidates(_ context.Context, _, after string, limit int) ([]state.WorkflowScheduleCandidate, error) {
	var result []state.WorkflowScheduleCandidate
	for _, item := range s.candidates {
		if item.AppID > after {
			result = append(result, item)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}
func (s *slowPageSchedules) AdmitScheduledWorkflow(_ context.Context, appID, _, _ string, _ time.Time) (state.WorkflowScheduleCursor, bool, error) {
	s.seen[appID]++
	return state.WorkflowScheduleCursor{}, false, nil
}
func TestWorkflowSchedulePassContinuesAcrossMinutes(t *testing.T) {
	store := &slowPageSchedules{MemStore: state.NewMemStore(), seen: map[string]int{}}
	for i := range api.WorkflowScheduleBatch + 1 {
		store.candidates = append(store.candidates, state.WorkflowScheduleCandidate{
			AppID:     fmt.Sprintf("app-%04d", i),
			Workflows: json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"* * * * *"},"steps":[{"name":"main","run":"report"}]}]`),
		})
	}
	engine, _ := sched.MakeEngine(t, statetest.NewMem(store), &sched.FakeWakeVMM{})
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	loop := sched.NewLoop(nil, engine, nil).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
	for range 2 {
		if err := loop.RunWorkflowSchedulesTick(t.Context()); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	last := store.candidates[len(store.candidates)-1].AppID
	if store.seen[last] != 1 {
		t.Fatalf("last candidate seen %d times", store.seen[last])
	}
}
