package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowOperationWindowUsesFixedCurrentAttemptBudget(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name                            string
		timeout, startedAgo, leaseAhead time.Duration
		wantDeadline, wantLease         time.Duration
		stale                           bool
		budget                          *time.Duration // ADR-20261016110803 remaining execution budget
	}{
		{"default", 0, time.Second, time.Minute, 29 * time.Second, 29 * time.Second, false, nil},
		{"captured timeout", time.Minute, time.Second, 10 * time.Second, 59 * time.Second, 10 * time.Second, false, nil},
		{"deadline caps lease", 2 * time.Second, time.Second, time.Minute, time.Second, time.Second, false, nil},
		{"deadline expired with live lease", time.Second, time.Second, time.Minute, 0, 0, true, nil},
		{"lease expired", time.Minute, time.Second, 0, 0, 0, true, nil},
		{"budget shortens timeout", time.Minute, time.Second, time.Minute, 9 * time.Second, 9 * time.Second, false, ptrDuration(10 * time.Second)},
		{"timeout shorter than budget", 5 * time.Second, time.Second, time.Minute, 4 * time.Second, 4 * time.Second, false, ptrDuration(time.Minute)},
		{"budget spent", time.Minute, time.Second, time.Minute, 0, 0, true, ptrDuration(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, _ := json.Marshal(api.WorkflowSpec{Steps: []api.WorkflowStepSpec{{Name: "collect", Timeout: tc.timeout}, {Name: "finish"}}})
			window, err := operationWorkflowWindow(snapshot, "collect", now.Add(-tc.startedAgo), now.Add(tc.leaseAhead), now, tc.budget)
			if tc.stale {
				if !errors.Is(err, ErrOperationStaleAttempt) {
					t.Fatal("expired time budget accepted", err)
				}
				return
			}
			if err != nil || !window.deadline.Equal(now.Add(tc.wantDeadline)) || !window.lease.Equal(now.Add(tc.wantLease)) {
				t.Fatalf("window=%+v %v", window, err)
			}
			if _, err := operationWorkflowWindow(snapshot, "unknown", now, now.Add(time.Hour), now, nil); !errors.Is(err, ErrOperationStaleAttempt) {
				t.Fatal("uncaptured action received authority", err)
			}
			if _, err := operationWorkflowWindow(snapshot, "collect", time.Time{}, now.Add(time.Hour), now, nil); !errors.Is(err, ErrOperationStaleAttempt) {
				t.Fatal("missing current attempt start received authority", err)
			}
		})
	}
}

func ptrDuration(d time.Duration) *time.Duration { return &d }
